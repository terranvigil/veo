// Package contextaware implements context-aware encoding optimization.
// It extends per-title encoding with device profiles, VMAF model selection,
// and multi-codec optimization to produce device-specific bitrate ladders.
//
// Key insight: the same encode looks different on different devices.
// A 720p encode at 1.5 Mbps might be "transparent" on a phone (VMAF 93
// with phone model) but mediocre on a TV (VMAF 85 with standard model).
// Context-aware encoding produces separate ladders per device class.
package contextaware

import (
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/ladder"
)

// DeviceClass identifies a viewing context.
type DeviceClass string

const (
	DeviceMobile  DeviceClass = "mobile"
	DeviceDesktop DeviceClass = "desktop"
	DeviceTV      DeviceClass = "tv"
	DeviceTV4K    DeviceClass = "tv_4k"
)

// Profile defines encoding constraints and quality targets for a device class.
type Profile struct {
	Name        string              `json:"name"`
	Device      DeviceClass         `json:"device"`
	Description string              `json:"description"`
	VMAFModel   string              `json:"vmaf_model"`  // VMAF model to use
	MaxRes      ffmpeg.Resolution   `json:"max_res"`     // maximum useful resolution
	Resolutions []ffmpeg.Resolution `json:"resolutions"` // resolutions to test
	Codecs      []ffmpeg.Codec      `json:"codecs"`      // preferred codecs (order = priority)
	LadderOpts  ladder.Opts         `json:"ladder_opts"` // ladder selection parameters
}

// MobileProfile returns a profile optimized for mobile viewing.
// Uses VMAF phone model (more forgiving), caps at 720p, prefers efficient codecs.
func MobileProfile() Profile {
	return Profile{
		Name:        "Mobile",
		Device:      DeviceMobile,
		Description: "Smartphones and small tablets. Screen <7 inches, typically viewed at arm's length.",
		VMAFModel:   "vmaf_v0.6.1", // phone model not available in all libvmaf builds
		MaxRes:      ffmpeg.Res720p,
		Resolutions: []ffmpeg.Resolution{
			ffmpeg.Res360p,
			ffmpeg.Res480p,
			ffmpeg.Res720p,
		},
		Codecs: []ffmpeg.Codec{
			ffmpeg.CodecSVTAV1, // best efficiency for bandwidth-constrained mobile
			ffmpeg.CodecX264,   // fallback for older devices
		},
		LadderOpts: ladder.Opts{
			NumRungs:   4,
			MinBitrate: 150,
			MaxBitrate: 3000,
			MinVMAF:    50,
			MaxVMAF:    95,
		},
	}
}

// DesktopProfile returns a profile for desktop/laptop viewing.
// Standard VMAF model, up to 1080p, balanced codec selection.
func DesktopProfile() Profile {
	return Profile{
		Name:        "Desktop",
		Device:      DeviceDesktop,
		Description: "Laptops and desktop monitors. 13-27 inch screens at 2-3 feet.",
		VMAFModel:   "vmaf_v0.6.1",
		MaxRes:      ffmpeg.Res1080p,
		Resolutions: []ffmpeg.Resolution{
			ffmpeg.Res480p,
			ffmpeg.Res720p,
			ffmpeg.Res1080p,
		},
		Codecs: []ffmpeg.Codec{
			ffmpeg.CodecSVTAV1,
			ffmpeg.CodecX265,
			ffmpeg.CodecX264,
		},
		LadderOpts: ladder.Opts{
			NumRungs:   6,
			MinBitrate: 200,
			MaxBitrate: 8000,
			MinVMAF:    40,
			MaxVMAF:    97,
		},
	}
}

// TVProfile returns a profile for TV viewing.
// Standard VMAF model, up to 1080p, wider bitrate range.
func TVProfile() Profile {
	return Profile{
		Name:        "TV (1080p)",
		Device:      DeviceTV,
		Description: "TVs and large displays. 40-65 inch screens at 6-10 feet.",
		VMAFModel:   "vmaf_v0.6.1",
		MaxRes:      ffmpeg.Res1080p,
		Resolutions: []ffmpeg.Resolution{
			ffmpeg.Res480p,
			ffmpeg.Res720p,
			ffmpeg.Res1080p,
		},
		Codecs: []ffmpeg.Codec{
			ffmpeg.CodecSVTAV1,
			ffmpeg.CodecX265,
			ffmpeg.CodecX264,
		},
		LadderOpts: ladder.Opts{
			NumRungs:   8,
			MinBitrate: 200,
			MaxBitrate: 12000,
			MinVMAF:    40,
			MaxVMAF:    97,
		},
	}
}

// TV4KProfile returns a profile for 4K TV viewing.
// Uses 4K VMAF model (stricter at high res), includes 2160p.
func TV4KProfile() Profile {
	return Profile{
		Name:        "TV (4K)",
		Device:      DeviceTV4K,
		Description: "4K TVs. 55-85 inch screens at 5-8 feet (1.5x screen height).",
		VMAFModel:   "vmaf_4k_v0.6.1",
		MaxRes:      ffmpeg.Res2160p,
		Resolutions: []ffmpeg.Resolution{
			ffmpeg.Res720p,
			ffmpeg.Res1080p,
			ffmpeg.Res1440p,
			ffmpeg.Res2160p,
		},
		Codecs: []ffmpeg.Codec{
			ffmpeg.CodecSVTAV1,
			ffmpeg.CodecX265,
		},
		LadderOpts: ladder.Opts{
			NumRungs:   8,
			MinBitrate: 1000,
			MaxBitrate: 25000,
			MinVMAF:    40,
			MaxVMAF:    97,
		},
	}
}

// AllProfiles returns all predefined device profiles.
func AllProfiles() []Profile {
	return []Profile{
		MobileProfile(),
		DesktopProfile(),
		TVProfile(),
		TV4KProfile(),
	}
}
