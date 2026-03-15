//go:build unit

package hull

import (
	"math"
	"testing"
)

func TestBDRate_AV1vsH264(t *testing.T) {
	// Synthetic R-D curves modeling typical H.264 vs AV1 behavior.
	// AV1 achieves same quality at ~50% less bitrate.
	h264 := []Point{
		{Bitrate: 500, VMAF: 70},
		{Bitrate: 1000, VMAF: 82},
		{Bitrate: 2000, VMAF: 90},
		{Bitrate: 4000, VMAF: 95},
	}
	av1 := []Point{
		{Bitrate: 250, VMAF: 70},
		{Bitrate: 500, VMAF: 82},
		{Bitrate: 1000, VMAF: 90},
		{Bitrate: 2000, VMAF: 95},
	}

	bdrate, err := BDRate(h264, av1)
	if err != nil {
		t.Fatalf("BDRate failed: %v", err)
	}

	// AV1 should be negative (more efficient) - around -50%
	t.Logf("BD-Rate (AV1 vs H.264): %.1f%%", bdrate)

	if bdrate >= 0 {
		t.Errorf("BD-Rate should be negative (AV1 more efficient), got %.1f%%", bdrate)
	}
	if bdrate > -30 {
		t.Errorf("BD-Rate should be at least -30%% for AV1 vs H.264, got %.1f%%", bdrate)
	}
}

func TestBDRate_SameCurve(t *testing.T) {
	// BD-Rate of a curve against itself should be ~0%
	curve := []Point{
		{Bitrate: 500, VMAF: 70},
		{Bitrate: 1000, VMAF: 82},
		{Bitrate: 2000, VMAF: 90},
		{Bitrate: 4000, VMAF: 95},
	}

	bdrate, err := BDRate(curve, curve)
	if err != nil {
		t.Fatalf("BDRate failed: %v", err)
	}

	t.Logf("BD-Rate (same curve): %.4f%%", bdrate)

	if math.Abs(bdrate) > 0.1 {
		t.Errorf("BD-Rate of same curve should be ~0%%, got %.4f%%", bdrate)
	}
}

func TestBDRate_TooFewPoints(t *testing.T) {
	short := []Point{
		{Bitrate: 500, VMAF: 70},
		{Bitrate: 1000, VMAF: 82},
		{Bitrate: 2000, VMAF: 90},
	}
	full := []Point{
		{Bitrate: 500, VMAF: 70},
		{Bitrate: 1000, VMAF: 82},
		{Bitrate: 2000, VMAF: 90},
		{Bitrate: 4000, VMAF: 95},
	}

	_, err := BDRate(short, full)
	if err == nil {
		t.Error("expected error for curve with < 4 points")
	}
}

func TestBDRate_NoOverlap(t *testing.T) {
	low := []Point{
		{Bitrate: 100, VMAF: 40},
		{Bitrate: 200, VMAF: 50},
		{Bitrate: 300, VMAF: 55},
		{Bitrate: 400, VMAF: 60},
	}
	high := []Point{
		{Bitrate: 1000, VMAF: 80},
		{Bitrate: 2000, VMAF: 88},
		{Bitrate: 3000, VMAF: 92},
		{Bitrate: 4000, VMAF: 95},
	}

	_, err := BDRate(low, high)
	if err == nil {
		t.Error("expected error for non-overlapping quality ranges")
	}
}

func TestBDRate_KnownResult(t *testing.T) {
	// With exact 50% bitrate reduction at every quality level,
	// BD-Rate should be exactly -50%
	curveA := []Point{
		{Bitrate: 1000, VMAF: 70},
		{Bitrate: 2000, VMAF: 80},
		{Bitrate: 4000, VMAF: 88},
		{Bitrate: 8000, VMAF: 94},
	}
	curveB := []Point{
		{Bitrate: 500, VMAF: 70},
		{Bitrate: 1000, VMAF: 80},
		{Bitrate: 2000, VMAF: 88},
		{Bitrate: 4000, VMAF: 94},
	}

	bdrate, err := BDRate(curveA, curveB)
	if err != nil {
		t.Fatalf("BDRate failed: %v", err)
	}

	t.Logf("BD-Rate (exact 50%% reduction): %.1f%%", bdrate)

	// Should be close to -50%
	if math.Abs(bdrate-(-50.0)) > 5.0 {
		t.Errorf("expected BD-Rate near -50%%, got %.1f%%", bdrate)
	}
}
