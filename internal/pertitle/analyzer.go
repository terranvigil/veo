// Package pertitle implements per-title encoding analysis. It orchestrates
// the full pipeline: define search space → parallel trial encodes → quality
// measurement → convex hull computation → ladder selection.
package pertitle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/terranvigil/veo/internal/checkpoint"
	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
	"github.com/terranvigil/veo/internal/quality"
)

// VideoEncoder encodes video files. Satisfied by ffmpeg.Encode.
type VideoEncoder func(ctx context.Context, job ffmpeg.EncodeJob, progress chan<- ffmpeg.Progress) (*ffmpeg.EncodeResult, error)

// VideoProber probes video file metadata. Satisfied by ffmpeg.Probe.
type VideoProber func(ctx context.Context, path string) (*ffmpeg.ProbeResult, error)

// QualityMeasurer measures quality between two videos. Satisfied by quality.Measure.
type QualityMeasurer func(ctx context.Context, reference, distorted string, opts quality.MeasureOpts) (*quality.Result, error)

// Config defines the search space and parameters for per-title analysis.
type Config struct {
	encoding.Config             // embedded common encoding config
	LadderOpts      ladder.Opts `json:"ladder_opts"`
	CheckpointPath  string      `json:"checkpoint_path,omitempty"`
	VMAFModel       string      `json:"vmaf_model,omitempty"` // VMAF model override (e.g. "vmaf_4k_v0.6.1")

	// lDependencies (if nil, use defaults)
	Encode  VideoEncoder    `json:"-"`
	Probe   VideoProber     `json:"-"`
	Measure QualityMeasurer `json:"-"`
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() Config {
	return Config{
		Config:     encoding.DefaultConfig(),
		LadderOpts: ladder.DefaultOpts(),
	}
}

// Result holds the complete output of a per-title analysis.
type Result struct {
	Source     string                      `json:"source"`
	SourceInfo *ffmpeg.ProbeResult         `json:"source_info"`
	Config     Config                      `json:"config"`
	Points     []hull.Point                `json:"points"`
	Hull       *hull.Hull                  `json:"hull"`
	PerCodec   map[ffmpeg.Codec]*hull.Hull `json:"per_codec_hulls"`
	Crossovers []hull.Crossover            `json:"crossovers"`
	Ladder     *ladder.Ladder              `json:"ladder"`
	Duration   time.Duration               `json:"duration"`
	TrialCount int                         `json:"trial_count"`
	Warnings   []string                    `json:"warnings,omitempty"`
}

// TrialProgress is sent for each completed trial encode.
type TrialProgress struct {
	Done       int
	Total      int
	Resolution ffmpeg.Resolution
	Codec      ffmpeg.Codec
	CRF        int
	Bitrate    float64
	VMAF       float64
	Error      string // non-empty if trial failed (continue-on-error mode)
}

// Analyze runs a full per-title analysis on the given source video.
// Progress updates are sent on the progress channel if non-nil.
func Analyze(ctx context.Context, source string, cfg Config, progress chan<- TrialProgress) (*Result, error) {
	start := time.Now()

	// wire defaults for dependencies
	probe := cfg.Probe
	if probe == nil {
		probe = ffmpeg.Probe
	}
	encode := cfg.Encode
	if encode == nil {
		encode = ffmpeg.Encode
	}
	measure := cfg.Measure
	if measure == nil {
		measure = quality.Measure
	}

	// validate input file exists
	if _, err := os.Stat(source); err != nil {
		return nil, fmt.Errorf("input file not found: %s", source)
	}

	// validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// probe source
	sourceInfo, err := probe(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("failed to probe source: %w", err)
	}

	video := sourceInfo.VideoStream()
	if video == nil {
		return nil, fmt.Errorf("no video stream found in %s", source)
	}
	if err := video.Validate(); err != nil {
		return nil, fmt.Errorf("invalid video stream in %s: %w", source, err)
	}

	// filter resolutions to those <= source resolution
	var resolutions []ffmpeg.Resolution
	for _, r := range cfg.Resolutions {
		if r.Height <= video.Height {
			resolutions = append(resolutions, r)
		}
	}
	if len(resolutions) == 0 {
		resolutions = cfg.Resolutions[:1] // use smallest if none fit
	}

	// build trial matrix
	type trial struct {
		Resolution ffmpeg.Resolution
		Codec      ffmpeg.Codec
		CRF        int
	}

	var trials []trial
	for _, res := range resolutions {
		for _, codec := range cfg.Codecs {
			for _, crf := range cfg.CRFValues {
				trials = append(trials, trial{Resolution: res, Codec: codec, CRF: crf})
			}
		}
	}

	// set up checkpointing if configured
	var cp *checkpoint.Checkpoint
	if cfg.CheckpointPath != "" {
		resStrs := make([]string, len(resolutions))
		for i, r := range resolutions {
			resStrs[i] = r.Label()
		}
		codecStrs := make([]string, len(cfg.Codecs))
		for i, c := range cfg.Codecs {
			codecStrs[i] = string(c)
		}
		hash := checkpoint.ConfigHash(source, resStrs, codecStrs, cfg.CRFValues, cfg.Preset)
		cp, err = checkpoint.New(cfg.CheckpointPath, hash, source)
		if err != nil {
			return nil, fmt.Errorf("failed to create checkpoint: %w", err)
		}
		if cp.CompletedCount() > 0 {
			slog.Info("resuming from checkpoint",
				"completed", cp.CompletedCount(),
				"total", len(trials))
		}
	}

	// create temp directory for trial encodes
	tmpDir, err := os.MkdirTemp("", "veo-pertitle-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// determine parallelism
	parallel := cfg.EffectiveParallel()

	// create progress sender (standardized non-blocking pattern)
	sender := encoding.NewProgressSender(progress)

	// probe cache to avoid redundant ffprobe calls during VMAF measurement
	probeCache := ffmpeg.NewProbeCache()

	var (
		mu       sync.Mutex
		points   []hull.Point
		warnings []string
		done     int
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallel)

	for _, t := range trials {
		g.Go(func() error {
			// lCheck if already completed (checkpoint resume)
			if cp != nil {
				if p, ok := cp.Get(t.Resolution.Label(), string(t.Codec), t.CRF); ok {
					mu.Lock()
					points = append(points, p)
					done++
					d := done
					mu.Unlock()
					sendProgress(sender, TrialProgress{
						Done: d, Total: len(trials),
						Resolution: t.Resolution, Codec: t.Codec, CRF: t.CRF,
						Bitrate: p.Bitrate, VMAF: p.VMAF,
					})
					return nil
				}
			}

			outPath := filepath.Join(tmpDir, fmt.Sprintf("%s_%s_crf%d.mp4",
				t.Resolution.Label(), t.Codec, t.CRF))

			job := ffmpeg.EncodeJob{
				Input:       source,
				Output:      outPath,
				Resolution:  t.Resolution,
				Codec:       t.Codec,
				CRF:         t.CRF,
				RateControl: cfg.RateControl,
				Preset:      encoding.PresetForCodec(t.Codec, cfg.Preset),
			}

			encResult, err := encode(gctx, job, nil)
			if err != nil {
				// continue on error: log warning, skip this trial
				msg := fmt.Sprintf("encode failed (%s %s CRF %d): %v",
					t.Resolution.Label(), t.Codec, t.CRF, err)
				slog.Warn(msg)
				mu.Lock()
				warnings = append(warnings, msg)
				done++
				mu.Unlock()
				sendProgress(sender, TrialProgress{
					Done: done, Total: len(trials),
					Resolution: t.Resolution, Codec: t.Codec, CRF: t.CRF,
					Error: msg,
				})
				return nil // don't abort other trials
			}

			// lValidate encode output before measuring
			if info, statErr := os.Stat(outPath); statErr != nil || info.Size() == 0 {
				msg := fmt.Sprintf("encode produced empty/missing output (%s %s CRF %d)",
					t.Resolution.Label(), t.Codec, t.CRF)
				slog.Warn(msg)
				mu.Lock()
				warnings = append(warnings, msg)
				done++
				mu.Unlock()
				return nil
			}

			// measure quality
			qResult, err := measure(gctx, source, outPath, quality.MeasureOpts{
				Metrics:    []quality.Metric{quality.MetricVMAF, quality.MetricPSNR},
				Subsample:  cfg.Subsample,
				Model:      cfg.VMAFModel,
				ProbeCache: probeCache,
			})
			if err != nil {
				msg := fmt.Sprintf("quality measurement failed (%s %s CRF %d): %v",
					t.Resolution.Label(), t.Codec, t.CRF, err)
				slog.Warn(msg)
				mu.Lock()
				warnings = append(warnings, msg)
				done++
				mu.Unlock()
				_ = os.Remove(outPath)
				sendProgress(sender, TrialProgress{
					Done: done, Total: len(trials),
					Resolution: t.Resolution, Codec: t.Codec, CRF: t.CRF,
					Error: msg,
				})
				return nil
			}

			// clean up encode output to save disk space
			_ = os.Remove(outPath)

			p := hull.Point{
				Resolution: t.Resolution,
				Codec:      t.Codec,
				CRF:        t.CRF,
				Bitrate:    encResult.Bitrate,
				VMAF:       qResult.VMAF,
				PSNR:       qResult.PSNR,
			}

			// sanity check results
			if p.VMAF <= 0 {
				msg := fmt.Sprintf("suspicious VMAF=%.1f for %s %s CRF %d (%.0f kbps)",
					p.VMAF, t.Resolution.Label(), t.Codec, t.CRF, p.Bitrate)
				slog.Warn(msg)
				mu.Lock()
				warnings = append(warnings, msg)
				mu.Unlock()
			}

			// save to checkpoint
			if cp != nil {
				if cpErr := cp.Save(t.Resolution.Label(), string(t.Codec), t.CRF, p); cpErr != nil {
					slog.Warn("checkpoint save failed", "error", cpErr)
				}
			}

			// store result - send progress AFTER releasing lock
			mu.Lock()
			points = append(points, p)
			done++
			d := done
			mu.Unlock()

			sendProgress(sender, TrialProgress{
				Done:       d,
				Total:      len(trials),
				Resolution: t.Resolution,
				Codec:      t.Codec,
				CRF:        t.CRF,
				Bitrate:    p.Bitrate,
				VMAF:       p.VMAF,
			})

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	if len(points) == 0 {
		return nil, fmt.Errorf("all %d trials failed; check warnings", len(trials))
	}

	// compute hulls
	allHull := hull.ComputeUpper(points)
	perCodec := hull.ComputePerCodec(points)
	crossovers := allHull.Crossovers()

	// select ladder
	selectedLadder := ladder.Select(allHull, cfg.LadderOpts)

	// clean up checkpoint on successful completion
	if cp != nil {
		_ = cp.Remove()
	}

	return &Result{
		Source:     source,
		SourceInfo: sourceInfo,
		Config:     cfg,
		Points:     points,
		Hull:       allHull,
		PerCodec:   perCodec,
		Crossovers: crossovers,
		Ladder:     selectedLadder,
		Duration:   time.Since(start),
		TrialCount: len(trials),
		Warnings:   warnings,
	}, nil
}

// uses the shared non-blocking sender pattern.
func sendProgress(sender *encoding.ProgressSender[TrialProgress], p TrialProgress) {
	sender.Send(p)
}

// SaveJSON writes the analysis result to a JSON file.
func (r *Result) SaveJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
