//go:build unit

package contextaware

import (
	"testing"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

func TestAllProfiles(t *testing.T) {
	profiles := AllProfiles()
	if len(profiles) != 4 {
		t.Fatalf("expected 4 profiles, got %d", len(profiles))
	}

	names := map[string]bool{}
	for _, p := range profiles {
		if names[p.Name] {
			t.Errorf("duplicate profile name: %s", p.Name)
		}
		names[p.Name] = true

		if len(p.Resolutions) == 0 {
			t.Errorf("profile %s has no resolutions", p.Name)
		}
		if len(p.Codecs) == 0 {
			t.Errorf("profile %s has no codecs", p.Name)
		}
		if p.VMAFModel == "" {
			t.Errorf("profile %s has no VMAF model", p.Name)
		}
		if p.LadderOpts.NumRungs <= 0 {
			t.Errorf("profile %s has invalid num rungs", p.Name)
		}
	}
}

func TestMobileProfile_Constraints(t *testing.T) {
	p := MobileProfile()

	// Mobile should cap at 720p
	if p.MaxRes.Height > 720 {
		t.Errorf("mobile max res %s exceeds 720p", p.MaxRes.Label())
	}

	// Should not include 1080p or higher in resolutions
	for _, r := range p.Resolutions {
		if r.Height > 720 {
			t.Errorf("mobile includes resolution %s (should cap at 720p)", r.Label())
		}
	}

	// Lower max bitrate than desktop/TV
	if p.LadderOpts.MaxBitrate > 5000 {
		t.Errorf("mobile max bitrate %.0f too high", p.LadderOpts.MaxBitrate)
	}
}

func TestTV4KProfile_Constraints(t *testing.T) {
	p := TV4KProfile()

	// 4K TV should include 2160p
	has2160 := false
	for _, r := range p.Resolutions {
		if r == ffmpeg.Res2160p {
			has2160 = true
		}
	}
	if !has2160 {
		t.Error("4K TV profile should include 2160p resolution")
	}

	// Should use 4K VMAF model
	if p.VMAFModel != "vmaf_4k_v0.6.1" {
		t.Errorf("4K TV should use vmaf_4k_v0.6.1, got %s", p.VMAFModel)
	}

	// Higher max bitrate
	if p.LadderOpts.MaxBitrate < 15000 {
		t.Errorf("4K TV max bitrate %.0f too low", p.LadderOpts.MaxBitrate)
	}
}

func TestProfileResolutionOrder(t *testing.T) {
	for _, p := range AllProfiles() {
		for i := 1; i < len(p.Resolutions); i++ {
			if p.Resolutions[i].Height < p.Resolutions[i-1].Height {
				t.Errorf("profile %s resolutions not ascending at index %d", p.Name, i)
			}
		}
	}
}
