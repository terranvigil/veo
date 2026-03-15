//go:build integration

package contextaware

import (
	"context"
	"os"
	"testing"
)

// Integration tests for context-aware encoding.
// Run with: make test-integration

const akiyoPath = "../../assets/sd/akiyo_cif.y4m"

func assetExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestMobileLadderUsesLessBandwidthThanDesktop verifies that the mobile
// ladder produces lower bitrate rungs than the desktop ladder for the same
// content. This is the core value of context-aware encoding.
func TestMobileLadderUsesLessBandwidthThanDesktop(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("akiyo not available")
	}

	cfg := Config{
		Profiles:  []Profile{MobileProfile(), DesktopProfile()},
		CRFValues: []int{22, 30, 38},
		Preset:    "ultrafast",
		Subsample: 5,
		Parallel:  2,
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("context-aware analysis failed: %v", err)
	}

	if len(result.Devices) != 2 {
		t.Fatalf("expected 2 device results, got %d", len(result.Devices))
	}

	mobile := result.Devices[0]
	desktop := result.Devices[1]

	if len(mobile.Ladder.Rungs) == 0 || len(desktop.Ladder.Rungs) == 0 {
		t.Skip("one or both ladders empty")
	}

	// Mobile top rung should have lower bitrate than desktop top rung
	mobileTop := mobile.Ladder.Rungs[len(mobile.Ladder.Rungs)-1]
	desktopTop := desktop.Ladder.Rungs[len(desktop.Ladder.Rungs)-1]

	t.Logf("Mobile top:  %s %.0f kbps VMAF %.1f",
		mobileTop.Resolution.Label(), mobileTop.Bitrate, mobileTop.VMAF)
	t.Logf("Desktop top: %s %.0f kbps VMAF %.1f",
		desktopTop.Resolution.Label(), desktopTop.Bitrate, desktopTop.VMAF)

	if mobileTop.Bitrate > desktopTop.Bitrate {
		t.Errorf("mobile top bitrate (%.0f) should not exceed desktop (%.0f)",
			mobileTop.Bitrate, desktopTop.Bitrate)
	}
}

// TestMobileCapsAtLowerResolution verifies that the mobile ladder doesn't
// include resolutions above 720p.
func TestMobileCapsAtLowerResolution(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("akiyo not available")
	}

	cfg := Config{
		Profiles:  []Profile{MobileProfile()},
		CRFValues: []int{22, 30, 38},
		Preset:    "ultrafast",
		Subsample: 5,
		Parallel:  2,
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	mobile := result.Devices[0]
	for _, rung := range mobile.Ladder.Rungs {
		if rung.Resolution.Height > 720 {
			t.Errorf("mobile ladder includes %s - should cap at 720p", rung.Resolution.Label())
		}
	}
}

// TestDeviceProfilesProduceDifferentLadders verifies that different device
// profiles produce meaningfully different ladders for the same content.
func TestDeviceProfilesProduceDifferentLadders(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("akiyo not available")
	}

	cfg := Config{
		Profiles:  []Profile{MobileProfile(), DesktopProfile()},
		CRFValues: []int{22, 30, 38},
		Preset:    "ultrafast",
		Subsample: 5,
		Parallel:  2,
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	mobile := result.Devices[0]
	desktop := result.Devices[1]

	// Ladders should differ in at least one of: rung count, max bitrate, or codecs
	if len(mobile.Ladder.Rungs) == len(desktop.Ladder.Rungs) {
		allSame := true
		for i := range mobile.Ladder.Rungs {
			if mobile.Ladder.Rungs[i].Bitrate != desktop.Ladder.Rungs[i].Bitrate {
				allSame = false
				break
			}
		}
		if allSame {
			t.Error("mobile and desktop produced identical ladders - context-aware is not differentiating")
		}
	}

	t.Logf("Mobile: %d rungs, Desktop: %d rungs", len(mobile.Ladder.Rungs), len(desktop.Ladder.Rungs))
}
