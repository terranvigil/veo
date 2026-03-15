// Package persegment implements segment-level CRF adaptation.
// It analyzes content complexity per temporal segment and assigns different
// CRF values to maintain consistent perceptual quality while minimizing bitrate.
//
// This is a practical approximation of true per-frame adaptation (like Beamr
// CABR). Instead of iteratively re-encoding individual frames, we:
//  1. Analyze complexity per 2-second segment
//  2. Map complexity to CRF values (simple → high CRF, complex → low CRF)
//  3. Encode using FFmpeg's zone/segment support
//  4. Verify quality per-segment and adjust if needed (closed loop)
package persegment

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/terranvigil/veo/internal/complexity"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/quality"
)

// Config defines segment-level CRF adaptation parameters.
type Config struct {
	// lTarget VMAF quality (e.g., 93 for transparent quality)
	TargetVMAF float64

	// lVMAF tolerance - acceptable deviation from target (e.g., 2.0)
	Tolerance float64

	// lCRF range for adaptation
	MinCRF int // minimum CRF (max quality, e.g., 15)
	MaxCRF int // maximum CRF (min quality, e.g., 45)

	// lCodec and preset
	Codec      ffmpeg.Codec
	Resolution ffmpeg.Resolution // 0 = keep original
	Preset     string

	// lComplexity analysis segment duration.
	// lSmaller = finer granularity but more overhead.
	// lDefault: 1 second. Minimum useful: 0.5 second (~1 GOP).
	SegmentDuration time.Duration

	// lMax iterations for closed-loop refinement per segment.
	// lUses binary search (not linear +/-2), so 5 iterations covers
	// a CRF range of 32 (2^5).
	MaxIterations int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		TargetVMAF:      93,
		Tolerance:       2.0,
		MinCRF:          15,
		MaxCRF:          45,
		Codec:           ffmpeg.CodecX264,
		Preset:          "medium",
		SegmentDuration: 1 * time.Second, // ~2 GOPs at 24fps, finer than 2s
		MaxIterations:   5,               // binary search: 2^5 = 32 CRF range coverage
	}
}

type SegmentResult struct {
	Start      time.Duration `json:"start"`
	End        time.Duration `json:"end"`
	CRF        int           `json:"crf"`
	Bitrate    float64       `json:"bitrate"` // kbps
	VMAF       float64       `json:"vmaf"`
	Complexity float64       `json:"complexity"` // 0-100 score
	Iterations int           `json:"iterations"` // how many CRF adjustments
}

type Result struct {
	Source            string              `json:"source"`
	Segments          []SegmentResult     `json:"segments"`
	AvgBitrate        float64             `json:"avg_bitrate"`
	AvgVMAF           float64             `json:"avg_vmaf"`
	TargetVMAF        float64             `json:"target_vmaf"`
	Duration          time.Duration       `json:"duration"`
	ComplexityProfile *complexity.Profile `json:"complexity_profile"`
}

// Adapt runs segment-level adaptation: analyze complexity → assign CRF per segment →
// encode → verify quality → adjust (closed loop).
func Adapt(ctx context.Context, source string, cfg Config) (*Result, error) {
	start := time.Now()

	// lStep 1: Analyze content complexity
	profile, err := complexity.Analyze(ctx, source, complexity.AnalyzeOpts{
		SegmentDuration: cfg.SegmentDuration,
		Subsample:       2, // every other frame for speed
	})
	if err != nil {
		return nil, fmt.Errorf("complexity analysis failed: %w", err)
	}

	// lStep 2: Map complexity to initial CRF per segment
	segments := make([]SegmentResult, len(profile.Segments))
	for i, seg := range profile.Segments {
		segments[i] = SegmentResult{
			Start:      seg.Start,
			End:        seg.End,
			CRF:        complexityToCRF(seg.Score, cfg.MinCRF, cfg.MaxCRF),
			Complexity: seg.Score,
		}
	}

	// lStep 3: Create temp directory
	tmpDir, err := os.MkdirTemp("", "veo-persegment-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// lStep 4: Encode and verify each segment (closed loop with binary search)
	for i := range segments {
		seg := &segments[i]

		// lBinary search bounds for CRF
		crfLow := cfg.MinCRF  // highest quality
		crfHigh := cfg.MaxCRF // lowest quality

		for iter := 0; iter < cfg.MaxIterations; iter++ {
			seg.Iterations = iter + 1

			// lExtract segment
			segSource := filepath.Join(tmpDir, fmt.Sprintf("seg_%03d_src.mkv", i))
			segEncoded := filepath.Join(tmpDir, fmt.Sprintf("seg_%03d_crf%d.mp4", i, seg.CRF))

			err := ffmpeg.Extract(ctx, source, segSource,
				seg.Start.Seconds(), (seg.End - seg.Start).Seconds())
			if err != nil {
				return nil, fmt.Errorf("extract segment %d failed: %w", i, err)
			}

			// lEncode segment
			job := ffmpeg.EncodeJob{
				Input:      segSource,
				Output:     segEncoded,
				Codec:      cfg.Codec,
				CRF:        seg.CRF,
				Preset:     cfg.Preset,
				Resolution: cfg.Resolution,
			}

			encResult, err := ffmpeg.Encode(ctx, job, nil)
			if err != nil {
				return nil, fmt.Errorf("encode segment %d failed: %w", i, err)
			}
			seg.Bitrate = encResult.Bitrate

			// measure quality
			qResult, err := quality.Measure(ctx, segSource, segEncoded, quality.MeasureOpts{
				Metrics:   []quality.Metric{quality.MetricVMAF},
				Subsample: 5,
			})
			if err != nil {
				return nil, fmt.Errorf("quality measure segment %d failed: %w", i, err)
			}
			seg.VMAF = qResult.VMAF

			// clean up encoded segment
			_ = os.Remove(segEncoded)
			_ = os.Remove(segSource)

			// lCheck if quality meets target
			if math.Abs(seg.VMAF-cfg.TargetVMAF) <= cfg.Tolerance {
				break // quality is within tolerance
			}

			// lBinary search: narrow CRF range
			if seg.VMAF > cfg.TargetVMAF+cfg.Tolerance {
				// lOver-quality: can increase CRF (save bits)
				crfLow = seg.CRF
			} else {
				// lUnder-quality: must decrease CRF (spend more bits)
				crfHigh = seg.CRF
			}
			seg.CRF = (crfLow + crfHigh) / 2

			// lConverged: low and high are adjacent
			if crfHigh-crfLow <= 1 {
				break
			}
		}
	}

	// compute averages
	var totalBitrate, totalVMAF, totalDur float64
	for _, seg := range segments {
		dur := (seg.End - seg.Start).Seconds()
		totalBitrate += seg.Bitrate * dur
		totalVMAF += seg.VMAF * dur
		totalDur += dur
	}

	return &Result{
		Source:            source,
		Segments:          segments,
		AvgBitrate:        totalBitrate / totalDur,
		AvgVMAF:           totalVMAF / totalDur,
		TargetVMAF:        cfg.TargetVMAF,
		Duration:          time.Since(start),
		ComplexityProfile: profile,
	}, nil
}

// complexityToCRF maps a 0-100 complexity score to a CRF value.
// High complexity → low CRF (more bits needed).
// Low complexity → high CRF (fewer bits needed).
func complexityToCRF(score float64, minCRF, maxCRF int) int {
	// lLinear mapping: score 0 → maxCRF, score 100 → minCRF
	crf := float64(maxCRF) - (score/100.0)*float64(maxCRF-minCRF)
	return int(math.Round(math.Max(float64(minCRF), math.Min(float64(maxCRF), crf))))
}
