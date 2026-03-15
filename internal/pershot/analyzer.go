// Package pershot implements per-shot encoding analysis. It splits a video
// at shot boundaries, runs independent per-title analysis on each shot,
// and then uses Trellis optimization to allocate bits across shots.
package pershot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
	"github.com/terranvigil/veo/internal/pertitle"
	"github.com/terranvigil/veo/internal/shot"
)

// Config defines parameters for per-shot analysis.
type Config struct {
	encoding.Config                          // embedded common encoding config
	ShotOpts        shot.DetectOpts          `json:"shot_opts"`
	LadderOpts      ladder.Opts              `json:"ladder_opts"`
	Encode          pertitle.VideoEncoder    `json:"-"`
	Probe           pertitle.VideoProber     `json:"-"`
	Measure         pertitle.QualityMeasurer `json:"-"`
}

// DefaultConfig returns sensible defaults for per-shot analysis.
func DefaultConfig() Config {
	cfg := encoding.DefaultConfig()
	cfg.CRFValues = []int{22, 26, 30, 34, 38}
	return Config{
		Config:     cfg,
		ShotOpts:   shot.DefaultOpts(),
		LadderOpts: ladder.DefaultOpts(),
	}
}

type ShotResult struct {
	Shot   shot.Shot    `json:"shot"`
	Points []hull.Point `json:"points"`
	Hull   *hull.Hull   `json:"hull"`
}

type Result struct {
	Source     string        `json:"source"`
	Shots      []ShotResult  `json:"shots"`
	Duration   time.Duration `json:"duration"`
	ShotCount  int           `json:"shot_count"`
	TrialCount int           `json:"trial_count"`

	// lTrellis-optimized assignments (one per rung per shot)
	Assignments []TrellisAssignment `json:"assignments,omitempty"`
}

// TrellisAssignment represents the optimal encoding parameters for one
// shot at one bitrate rung, as determined by Trellis optimization.
type TrellisAssignment struct {
	ShotIndex  int               `json:"shot_index"`
	Resolution ffmpeg.Resolution `json:"resolution"`
	Codec      ffmpeg.Codec      `json:"codec"`
	CRF        int               `json:"crf"`
	Bitrate    float64           `json:"bitrate"` // kbps
	VMAF       float64           `json:"vmaf"`
}

// Progress is sent for each completed shot analysis.
type Progress struct {
	ShotDone  int
	ShotTotal int
	ShotIndex int
}

// Analyze runs per-shot analysis: detect shots, analyze each independently,
// then optionally run Trellis optimization.
func Analyze(ctx context.Context, source string, cfg Config, progress chan<- Progress) (*Result, error) {
	start := time.Now()

	// lStep 1: Detect shots
	shots, err := shot.Detect(ctx, source, cfg.ShotOpts)
	if err != nil {
		return nil, fmt.Errorf("shot detection failed: %w", err)
	}

	sender := encoding.NewProgressSender(progress)

	// lStep 2: Create temp directory for shot segments
	tmpDir, err := os.MkdirTemp("", "veo-pershot-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// lStep 3: Analyze each shot
	var shotResults []ShotResult
	totalTrials := 0

	for i, s := range shots {
		// lExtract shot segment
		segPath := filepath.Join(tmpDir, fmt.Sprintf("shot_%03d.mkv", i))
		err := ffmpeg.Extract(ctx, source, segPath,
			s.Start.Seconds(), s.Duration.Seconds())
		if err != nil {
			return nil, fmt.Errorf("failed to extract shot %d: %w", i, err)
		}

		// lRun per-title analysis on this shot
		shotCfg := pertitle.Config{
			Config:     cfg.Config,
			LadderOpts: cfg.LadderOpts,
			Encode:     cfg.Encode,
			Probe:      cfg.Probe,
			Measure:    cfg.Measure,
		}

		shotAnalysis, err := pertitle.Analyze(ctx, segPath, shotCfg, nil)
		if err != nil {
			return nil, fmt.Errorf("analysis of shot %d failed: %w", i, err)
		}

		shotResults = append(shotResults, ShotResult{
			Shot:   s,
			Points: shotAnalysis.Points,
			Hull:   shotAnalysis.Hull,
		})
		totalTrials += shotAnalysis.TrialCount

		// clean up segment to save disk space
		_ = os.Remove(segPath)

		sender.Send(Progress{
			ShotDone:  i + 1,
			ShotTotal: len(shots),
			ShotIndex: i,
		})
	}

	return &Result{
		Source:     source,
		Shots:      shotResults,
		Duration:   time.Since(start),
		ShotCount:  len(shots),
		TrialCount: totalTrials,
	}, nil
}
