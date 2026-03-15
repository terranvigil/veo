//go:build unit

package complexity

import (
	"testing"
	"time"
)

func TestParseComplexityOutput(t *testing.T) {
	output := `frame:0    pts:0       pts_time:0
lavfi.entropy.normalized_entropy.normal.Y=0.893578
lavfi.signalstats.YDIF=0
frame:1    pts:1       pts_time:0.04
lavfi.entropy.normalized_entropy.normal.Y=0.891234
lavfi.signalstats.YDIF=1.5
frame:2    pts:2       pts_time:0.08
lavfi.entropy.normalized_entropy.normal.Y=0.902000
lavfi.signalstats.YDIF=3.2
`
	frames := parseComplexityOutput(output)

	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}

	if frames[0].Spatial != 0.893578 {
		t.Errorf("frame 0 spatial = %f, want 0.893578", frames[0].Spatial)
	}
	if frames[0].Temporal != 0 {
		t.Errorf("frame 0 temporal = %f, want 0", frames[0].Temporal)
	}
	if frames[1].Temporal != 1.5 {
		t.Errorf("frame 1 temporal = %f, want 1.5", frames[1].Temporal)
	}
	if frames[2].Spatial != 0.902 {
		t.Errorf("frame 2 spatial = %f, want 0.902", frames[2].Spatial)
	}
}

func TestParseComplexityOutput_Empty(t *testing.T) {
	frames := parseComplexityOutput("")
	if len(frames) != 0 {
		t.Errorf("expected 0 frames, got %d", len(frames))
	}
}

func TestAggregateSegments(t *testing.T) {
	frames := []FrameComplexity{
		{PTS: 0, Spatial: 0.8, Temporal: 2.0},
		{PTS: time.Second, Spatial: 0.85, Temporal: 3.0},
		{PTS: 2 * time.Second, Spatial: 0.9, Temporal: 10.0},
		{PTS: 3 * time.Second, Spatial: 0.95, Temporal: 15.0},
	}

	segments := aggregateSegments(frames, 4*time.Second, 2*time.Second)

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	// Segment 0: frames at 0s and 1s
	if segments[0].AvgSpatial != 0.825 {
		t.Errorf("seg 0 avg spatial = %f, want 0.825", segments[0].AvgSpatial)
	}
	if segments[0].AvgTemporal != 2.5 {
		t.Errorf("seg 0 avg temporal = %f, want 2.5", segments[0].AvgTemporal)
	}

	// Segment 1: frames at 2s and 3s - more complex
	if segments[1].AvgSpatial != 0.925 {
		t.Errorf("seg 1 avg spatial = %f, want 0.925", segments[1].AvgSpatial)
	}
	if segments[1].Score > segments[0].Score {
		t.Logf("seg 1 score (%.1f) > seg 0 score (%.1f) - correct, more complex",
			segments[1].Score, segments[0].Score)
	} else {
		t.Errorf("seg 1 should have higher score than seg 0")
	}
}

func TestComputeScore(t *testing.T) {
	// Low complexity: low entropy, low motion
	low := computeScore(0.6, 1.0)
	// High complexity: high entropy, high motion
	high := computeScore(0.95, 25.0)

	if high <= low {
		t.Errorf("high complexity score (%.1f) should exceed low (%.1f)", high, low)
	}
	if low < 0 || low > 100 {
		t.Errorf("score should be 0-100, got %.1f", low)
	}
	if high < 0 || high > 100 {
		t.Errorf("score should be 0-100, got %.1f", high)
	}

	t.Logf("Low complexity: %.1f, High complexity: %.1f", low, high)
}
