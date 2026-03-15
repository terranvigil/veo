//go:build unit

package ladder

import (
	"testing"

	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
)

func TestSelect_BasicLadder(t *testing.T) {
	h := &hull.Hull{
		Points: []hull.Point{
			{Bitrate: 200, VMAF: 55, Resolution: ffmpeg.Res480p, CRF: 38},
			{Bitrate: 400, VMAF: 70, Resolution: ffmpeg.Res480p, CRF: 30},
			{Bitrate: 800, VMAF: 82, Resolution: ffmpeg.Res720p, CRF: 28},
			{Bitrate: 1500, VMAF: 90, Resolution: ffmpeg.Res720p, CRF: 22},
			{Bitrate: 3000, VMAF: 95, Resolution: ffmpeg.Res1080p, CRF: 22},
			{Bitrate: 5000, VMAF: 97, Resolution: ffmpeg.Res1080p, CRF: 18},
		},
	}

	l := Select(h, Opts{
		NumRungs:   4,
		MinBitrate: 200,
		MaxBitrate: 6000,
		MinVMAF:    50,
		MaxVMAF:    97,
	})

	if len(l.Rungs) != 4 {
		t.Fatalf("expected 4 rungs, got %d", len(l.Rungs))
	}

	// Rungs should be sorted by bitrate
	for i := 1; i < len(l.Rungs); i++ {
		if l.Rungs[i].Bitrate < l.Rungs[i-1].Bitrate {
			t.Errorf("rungs not sorted by bitrate at index %d", i)
		}
	}

	t.Logf("Selected ladder:")
	for _, r := range l.Rungs {
		t.Logf("  %s %.0f kbps VMAF %.1f", r.Resolution.Label(), r.Bitrate, r.VMAF)
	}
}

func TestSelect_EmptyHull(t *testing.T) {
	l := Select(&hull.Hull{}, DefaultOpts())
	if len(l.Rungs) != 0 {
		t.Errorf("expected 0 rungs for empty hull, got %d", len(l.Rungs))
	}
}

func TestSelect_FewerPointsThanRungs(t *testing.T) {
	h := &hull.Hull{
		Points: []hull.Point{
			{Bitrate: 500, VMAF: 80},
			{Bitrate: 2000, VMAF: 95},
		},
	}

	l := Select(h, Opts{
		NumRungs:   6,
		MinBitrate: 100,
		MaxBitrate: 10000,
		MinVMAF:    40,
	})

	// Should return all available points (2)
	if len(l.Rungs) != 2 {
		t.Errorf("expected 2 rungs (all available), got %d", len(l.Rungs))
	}
}

func TestSelect_BitrateConstraints(t *testing.T) {
	h := &hull.Hull{
		Points: []hull.Point{
			{Bitrate: 50, VMAF: 40},
			{Bitrate: 100, VMAF: 55},
			{Bitrate: 500, VMAF: 80},
			{Bitrate: 2000, VMAF: 92},
			{Bitrate: 5000, VMAF: 96},
			{Bitrate: 10000, VMAF: 98},
		},
	}

	l := Select(h, Opts{
		NumRungs:   4,
		MinBitrate: 200,
		MaxBitrate: 6000,
		MinVMAF:    50,
	})

	for _, r := range l.Rungs {
		if r.Bitrate < 200 {
			t.Errorf("rung bitrate %.0f below minimum 200", r.Bitrate)
		}
		if r.Bitrate > 6000 {
			t.Errorf("rung bitrate %.0f above maximum 6000", r.Bitrate)
		}
		if r.VMAF < 50 {
			t.Errorf("rung VMAF %.1f below minimum 50", r.VMAF)
		}
	}
}

func TestBitrateRange(t *testing.T) {
	l := &Ladder{
		Rungs: []Rung{
			{Point: hull.Point{Bitrate: 200}},
			{Point: hull.Point{Bitrate: 1000}},
			{Point: hull.Point{Bitrate: 5000}},
		},
	}

	min, max := l.BitrateRange()
	if min != 200 {
		t.Errorf("min bitrate = %.0f, want 200", min)
	}
	if max != 5000 {
		t.Errorf("max bitrate = %.0f, want 5000", max)
	}
}

func TestSelect_CrossoverEnforcement(t *testing.T) {
	// Hull with crossover: 480p→720p at ~750 kbps, 720p→1080p at ~1750 kbps.
	h := &hull.Hull{
		Points: []hull.Point{
			{Bitrate: 200, VMAF: 50, Resolution: ffmpeg.Res480p},
			{Bitrate: 500, VMAF: 70, Resolution: ffmpeg.Res480p},
			// crossover 480p→720p between 500 and 1000
			{Bitrate: 1000, VMAF: 85, Resolution: ffmpeg.Res720p},
			{Bitrate: 1500, VMAF: 90, Resolution: ffmpeg.Res720p},
			// crossover 720p→1080p between 1500 and 2000
			{Bitrate: 2000, VMAF: 93, Resolution: ffmpeg.Res1080p},
			{Bitrate: 3000, VMAF: 96, Resolution: ffmpeg.Res1080p},
		},
	}

	// Verify crossovers are detected
	crossovers := h.Crossovers()
	if len(crossovers) != 2 {
		t.Fatalf("expected 2 crossovers, got %d", len(crossovers))
	}

	l := Select(h, Opts{
		NumRungs:   4,
		MinBitrate: 100,
		MaxBitrate: 5000,
		MinVMAF:    40,
	})

	// Verify monotonic resolution: rungs should not decrease in resolution
	for i := 1; i < len(l.Rungs); i++ {
		if l.Rungs[i].Resolution.Height < l.Rungs[i-1].Resolution.Height {
			t.Errorf("resolution decreased from %s to %s at rung %d",
				l.Rungs[i-1].Resolution.Label(), l.Rungs[i].Resolution.Label(), i+1)
		}
	}

	t.Logf("Ladder with crossover enforcement:")
	for _, r := range l.Rungs {
		t.Logf("  %s %.0f kbps VMAF %.1f", r.Resolution.Label(), r.Bitrate, r.VMAF)
	}
}

func TestNetflixOldLadder(t *testing.T) {
	nf := NetflixOld()

	if len(nf.Rungs) != 10 {
		t.Errorf("Netflix fixed ladder should have 10 rungs, got %d", len(nf.Rungs))
	}
	if nf.TopBitrate() != 5800 {
		t.Errorf("top bitrate = %.0f, want 5800", nf.TopBitrate())
	}
	if nf.Rungs[0].Bitrate != 235 {
		t.Errorf("bottom rung = %.0f kbps, want 235", nf.Rungs[0].Bitrate)
	}
}
