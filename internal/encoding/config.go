// Package encoding provides shared types and utilities used across
// all encoding optimization packages (pertitle, pershot, persegment, contextaware).
package encoding

import (
	"fmt"
	"runtime"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

// Config holds common encoding parameters shared across all optimization modes.
// This struct is embedded (not composed) in package-specific configs for
// convenience - promoted fields allow direct access (cfg.Preset vs
// cfg.Config.Preset). The tradeoff is implicit field promotion, which
// is acceptable for this internal-only type.
type Config struct {
	Resolutions []ffmpeg.Resolution    `json:"resolutions"`
	CRFValues   []int                  `json:"crf_values"`
	Codecs      []ffmpeg.Codec         `json:"codecs"`
	Preset      string                 `json:"preset"`       // encoding speed preset
	Subsample   int                    `json:"subsample"`    // VMAF frame subsampling (0 = every frame)
	Parallel    int                    `json:"parallel"`     // max parallel encodes (0 = auto)
	RateControl ffmpeg.RateControlMode `json:"rate_control"` // "" = CRF (default), "qp" = fixed QP
}

// DefaultConfig returns sensible defaults for encoding analysis.
func DefaultConfig() Config {
	return Config{
		Resolutions: []ffmpeg.Resolution{
			ffmpeg.Res480p,
			ffmpeg.Res720p,
			ffmpeg.Res1080p,
		},
		CRFValues: []int{18, 22, 26, 30, 34, 38, 42},
		Codecs:    []ffmpeg.Codec{ffmpeg.CodecX264},
		Preset:    "veryfast",
		Subsample: 5,
		Parallel:  0,
	}
}

// Validate checks that the config has sane values. Should be called
// at the start of any analysis to fail fast on bad inputs.
func (c Config) Validate() error {
	if len(c.Resolutions) == 0 {
		return fmt.Errorf("must specify at least one resolution")
	}
	if len(c.CRFValues) == 0 {
		return fmt.Errorf("must specify at least one CRF value")
	}
	if len(c.Codecs) == 0 {
		return fmt.Errorf("must specify at least one codec")
	}
	for _, codec := range c.Codecs {
		switch codec {
		case ffmpeg.CodecX264, ffmpeg.CodecX265, ffmpeg.CodecSVTAV1:
		default:
			return fmt.Errorf("unknown codec: %s", codec)
		}
	}
	if c.Subsample < 0 {
		return fmt.Errorf("subsample must be >= 0, got %d", c.Subsample)
	}
	return nil
}

// EffectiveParallel returns the actual parallelism to use.
// If Parallel is 0, uses NumCPU/2 with a floor of 2.
func (c Config) EffectiveParallel() int {
	if c.Parallel > 0 {
		return c.Parallel
	}
	p := runtime.NumCPU() / 2
	if p < 2 {
		return 2
	}
	return p
}

// PresetForCodec maps a generic preset name to codec-specific presets.
// x264/x265 use named presets (ultrafast, veryfast, medium, slow, etc.)
// SVT-AV1 uses numeric presets (0=slowest/best to 13=fastest).
func PresetForCodec(codec ffmpeg.Codec, preset string) string {
	if codec != ffmpeg.CodecSVTAV1 {
		return preset
	}
	switch preset {
	case "ultrafast":
		return "12"
	case "superfast":
		return "11"
	case "veryfast":
		return "10"
	case "faster":
		return "9"
	case "fast":
		return "8"
	case "medium":
		return "6"
	case "slow":
		return "4"
	case "slower":
		return "2"
	case "veryslow":
		return "0"
	default:
		return preset
	}
}
