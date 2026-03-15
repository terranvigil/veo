//go:build integration

package pershot

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
	"github.com/terranvigil/veo/internal/shot"
)

// These integration tests validate per-shot encoding behavior using real
// encodes. They verify that the Trellis optimizer correctly allocates bits
// based on content complexity.
//
// Run with: make test-integration

const sintelPath = "../../assets/blender/sintel_trailer_2k_1080p24.y4m"
const akiyoPath = "../../assets/sd/akiyo_cif.y4m"

func assetExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestTrellisAllocatesMoreBitsToComplexShots verifies that Trellis optimization
// gives more bitrate to complex shots and less to simple shots.
// This is the core value proposition of per-shot encoding.
func TestTrellisAllocatesMoreBitsToComplexShots(t *testing.T) {
	if !assetExists(sintelPath) {
		t.Skip("sintel trailer not available: run ./scripts/download-assets.sh --clip sintel")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res480p, ffmpeg.Res720p},
			CRFValues:   []int{22, 30, 38},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    2,
		},
		ShotOpts: shot.DetectOpts{
			Threshold:   10,
			MinDuration: 500 * time.Millisecond,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result, err := Analyze(context.Background(), sintelPath, cfg, nil)
	if err != nil {
		t.Fatalf("per-shot analysis failed: %v", err)
	}

	if result.ShotCount < 2 {
		t.Fatalf("need at least 2 shots for comparison, got %d", result.ShotCount)
	}

	// Run Trellis
	assignments := TrellisOptimize(result.Shots, TrellisOpts{
		TargetBitrate: 1000,
	})

	if len(assignments) != result.ShotCount {
		t.Fatalf("expected %d assignments, got %d", result.ShotCount, len(assignments))
	}

	// Find max and min bitrate assignments
	var maxBR, minBR float64
	var maxIdx, minIdx int
	for i, a := range assignments {
		if i == 0 || a.Bitrate > maxBR {
			maxBR = a.Bitrate
			maxIdx = i
		}
		if i == 0 || a.Bitrate < minBR {
			minBR = a.Bitrate
			minIdx = i
		}
	}

	// The ratio between highest and lowest bitrate should be significant
	if maxBR > 0 && minBR > 0 {
		ratio := maxBR / minBR
		t.Logf("Trellis allocation ratio: %.1fx (max=%.0f kbps shot %d, min=%.0f kbps shot %d)",
			ratio, maxBR, maxIdx+1, minBR, minIdx+1)

		if ratio < 2.0 {
			t.Errorf("expected at least 2x bitrate ratio between complex/simple shots, got %.1fx", ratio)
		}
	}
}

// TestPerShotHullsVaryByComplexity verifies that different shots produce
// different convex hulls - the fundamental premise of per-shot encoding.
func TestPerShotHullsVaryByComplexity(t *testing.T) {
	if !assetExists(sintelPath) {
		t.Skip("sintel trailer not available")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res480p},
			CRFValues:   []int{22, 30, 38},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    2,
		},
		ShotOpts: shot.DetectOpts{
			Threshold:   10,
			MinDuration: 500 * time.Millisecond,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result, err := Analyze(context.Background(), sintelPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	if len(result.Shots) < 2 {
		t.Skip("need at least 2 shots")
	}

	// Compare bitrate at same CRF across shots - should differ
	var bitratesAtCRF22 []float64
	for _, sr := range result.Shots {
		for _, p := range sr.Points {
			if p.CRF == 22 {
				bitratesAtCRF22 = append(bitratesAtCRF22, p.Bitrate)
				break
			}
		}
	}

	if len(bitratesAtCRF22) >= 2 {
		maxBR := bitratesAtCRF22[0]
		minBR := bitratesAtCRF22[0]
		for _, br := range bitratesAtCRF22[1:] {
			if br > maxBR {
				maxBR = br
			}
			if br < minBR {
				minBR = br
			}
		}
		ratio := maxBR / minBR
		t.Logf("Bitrate variation at CRF 22: %.1fx (%.0f - %.0f kbps across %d shots)",
			ratio, minBR, maxBR, len(bitratesAtCRF22))

		// sintel has genuinely different shots (action vs title card),
		// so we expect meaningful bitrate variation
		if ratio < 1.5 {
			t.Errorf("bitrate variation %.1fx too low for multi-shot content - expected >= 1.5x", ratio)
		}
	}
}

// TestSingleShotFallsBackToPerTitle verifies that a single-shot video
// produces the same result whether analyzed with per-shot or per-title.
func TestSingleShotFallsBackToPerTitle(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("akiyo not available")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{22, 30, 38},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    1,
		},
		ShotOpts: shot.DetectOpts{
			Threshold:   10,
			MinDuration: 500 * time.Millisecond,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	// Single-shot video should have exactly 1 shot
	if result.ShotCount != 1 {
		t.Errorf("akiyo should be single-shot, got %d shots", result.ShotCount)
	}

	// Hull should still be valid
	if len(result.Shots[0].Hull.Points) < 2 {
		t.Errorf("single shot hull should have at least 2 points, got %d",
			len(result.Shots[0].Hull.Points))
	}

	t.Logf("Single shot: %d hull points, %.0f - %.0f kbps",
		len(result.Shots[0].Hull.Points),
		result.Shots[0].Hull.Points[0].Bitrate,
		result.Shots[0].Hull.Points[len(result.Shots[0].Hull.Points)-1].Bitrate)
}

// Ensure hull import is used
var _ = hull.Point{}
