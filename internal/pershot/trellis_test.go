//go:build unit

package pershot

import (
	"math"
	"testing"
	"time"

	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/shot"
)

func TestTrellisOptimize_EqualShots(t *testing.T) {
	// Two identical shots should get equal bitrate allocation
	hullPoints := []hull.Point{
		{Bitrate: 500, VMAF: 70, Resolution: ffmpeg.Res480p, CRF: 38},
		{Bitrate: 1000, VMAF: 85, Resolution: ffmpeg.Res720p, CRF: 30},
		{Bitrate: 2000, VMAF: 93, Resolution: ffmpeg.Res1080p, CRF: 22},
	}

	shotResults := []ShotResult{
		{
			Shot: shot.Shot{Index: 0, Duration: 5 * time.Second},
			Hull: &hull.Hull{Points: hullPoints},
		},
		{
			Shot: shot.Shot{Index: 1, Duration: 5 * time.Second},
			Hull: &hull.Hull{Points: hullPoints},
		},
	}

	assignments := TrellisOptimize(shotResults, TrellisOpts{
		TargetBitrate: 1000,
	})

	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(assignments))
	}

	// Equal shots with equal duration → equal assignments
	if assignments[0].Bitrate != assignments[1].Bitrate {
		t.Errorf("equal shots should get equal bitrate: shot 0 = %.0f, shot 1 = %.0f",
			assignments[0].Bitrate, assignments[1].Bitrate)
	}

	t.Logf("Target: 1000 kbps")
	for _, a := range assignments {
		t.Logf("  Shot %d: %s CRF %d, %.0f kbps, VMAF %.1f",
			a.ShotIndex, a.Resolution.Label(), a.CRF, a.Bitrate, a.VMAF)
	}
}

func TestTrellisOptimize_DifferentComplexity(t *testing.T) {
	// Simple shot (talking head) and complex shot (action)
	// The complex shot should get more bits
	simpleHull := []hull.Point{
		{Bitrate: 200, VMAF: 80, Resolution: ffmpeg.Res480p, CRF: 38},
		{Bitrate: 500, VMAF: 92, Resolution: ffmpeg.Res720p, CRF: 30},
		{Bitrate: 1000, VMAF: 97, Resolution: ffmpeg.Res1080p, CRF: 22},
	}
	complexHull := []hull.Point{
		{Bitrate: 800, VMAF: 60, Resolution: ffmpeg.Res480p, CRF: 38},
		{Bitrate: 2000, VMAF: 80, Resolution: ffmpeg.Res720p, CRF: 30},
		{Bitrate: 4000, VMAF: 92, Resolution: ffmpeg.Res1080p, CRF: 22},
	}

	shotResults := []ShotResult{
		{
			Shot: shot.Shot{Index: 0, Duration: 5 * time.Second},
			Hull: &hull.Hull{Points: simpleHull},
		},
		{
			Shot: shot.Shot{Index: 1, Duration: 5 * time.Second},
			Hull: &hull.Hull{Points: complexHull},
		},
	}

	assignments := TrellisOptimize(shotResults, TrellisOpts{
		TargetBitrate: 1500,
	})

	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(assignments))
	}

	t.Logf("Target: 1500 kbps avg")
	for _, a := range assignments {
		t.Logf("  Shot %d: %s CRF %d, %.0f kbps, VMAF %.1f",
			a.ShotIndex, a.Resolution.Label(), a.CRF, a.Bitrate, a.VMAF)
	}

	// Weighted average should be near target
	avg := (assignments[0].Bitrate + assignments[1].Bitrate) / 2
	if math.Abs(avg-1500)/1500 > 0.15 {
		t.Errorf("weighted avg bitrate %.0f too far from target 1500", avg)
	}
}

func TestTrellisOptimize_Empty(t *testing.T) {
	assignments := TrellisOptimize(nil, TrellisOpts{TargetBitrate: 1000})
	if len(assignments) != 0 {
		t.Errorf("expected 0 assignments for nil input, got %d", len(assignments))
	}
}

func TestTrellisOptimize_ZeroTarget(t *testing.T) {
	assignments := TrellisOptimize([]ShotResult{{}}, TrellisOpts{TargetBitrate: 0})
	if len(assignments) != 0 {
		t.Errorf("expected 0 assignments for zero target, got %d", len(assignments))
	}
}
