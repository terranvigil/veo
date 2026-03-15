package contextaware

import (
	"context"
	"fmt"
	"time"

	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
	"github.com/terranvigil/veo/internal/pertitle"
)

// Config defines parameters for context-aware analysis.
type Config struct {
	Profiles  []Profile // device profiles to analyze
	CRFValues []int     // CRF search space
	Preset    string    // encoding preset
	Subsample int       // VMAF frame subsampling
	Parallel  int       // max parallel encodes
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Profiles:  AllProfiles(),
		CRFValues: []int{18, 22, 26, 30, 34, 38, 42},
		Preset:    "veryfast",
		Subsample: 5,
		Parallel:  2,
	}
}

// DeviceResult holds the analysis result for one device profile.
type DeviceResult struct {
	Profile    Profile        `json:"profile"`
	Hull       *hull.Hull     `json:"hull"`
	Ladder     *ladder.Ladder `json:"ladder"`
	Points     []hull.Point   `json:"points"`
	TrialCount int            `json:"trial_count"`
}

// Result holds the complete context-aware analysis output.
type Result struct {
	Source   string         `json:"source"`
	Devices  []DeviceResult `json:"devices"`
	Duration time.Duration  `json:"duration"`
}

// Progress is sent for each completed device profile.
type Progress struct {
	DeviceDone  int
	DeviceTotal int
	DeviceName  string
}

// Analyze runs per-title analysis for each device profile, producing
// device-specific ladders optimized for each viewing context.
func Analyze(ctx context.Context, source string, cfg Config, progress chan<- Progress) (*Result, error) {
	start := time.Now()

	var devices []DeviceResult
	sender := encoding.NewProgressSender(progress)

	for i, profile := range cfg.Profiles {
		// lBuild per-title config from profile
		ptCfg := pertitle.Config{
			Config: encoding.Config{
				Resolutions: profile.Resolutions,
				CRFValues:   cfg.CRFValues,
				Codecs:      profile.Codecs,
				Preset:      cfg.Preset,
				Subsample:   cfg.Subsample,
				Parallel:    cfg.Parallel,
			},
			LadderOpts: profile.LadderOpts,
			VMAFModel:  profile.VMAFModel,
		}

		// lRun per-title analysis
		ptResult, err := pertitle.Analyze(ctx, source, ptCfg, nil)
		if err != nil {
			return nil, fmt.Errorf("analysis for %s failed: %w", profile.Name, err)
		}

		// select ladder with profile-specific constraints
		deviceLadder := ladder.Select(ptResult.Hull, profile.LadderOpts)

		devices = append(devices, DeviceResult{
			Profile:    profile,
			Hull:       ptResult.Hull,
			Ladder:     deviceLadder,
			Points:     ptResult.Points,
			TrialCount: ptResult.TrialCount,
		})

		sender.Send(Progress{
			DeviceDone:  i + 1,
			DeviceTotal: len(cfg.Profiles),
			DeviceName:  profile.Name,
		})
	}

	return &Result{
		Source:   source,
		Devices:  devices,
		Duration: time.Since(start),
	}, nil
}
