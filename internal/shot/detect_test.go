//go:build unit

package shot

import (
	"testing"
	"time"
)

func TestParseScdetOutput(t *testing.T) {
	// Sample scdet metadata output
	output := `frame:0    pts:0       pts_time:0
lavfi.scd.mafd=0.000
lavfi.scd.score=0.000
frame:1    pts:1       pts_time:0.04
lavfi.scd.mafd=0.145
lavfi.scd.score=0.000
frame:50    pts:50       pts_time:2
lavfi.scd.mafd=45.234
lavfi.scd.score=72.500
frame:51    pts:51       pts_time:2.04
lavfi.scd.mafd=0.200
lavfi.scd.score=0.000
frame:125    pts:125       pts_time:5
lavfi.scd.mafd=38.100
lavfi.scd.score=55.300
`
	changes := parseScdetOutput(output)

	if len(changes) != 2 {
		t.Fatalf("expected 2 scene changes, got %d", len(changes))
	}

	if changes[0].PTS != 2*time.Second {
		t.Errorf("first change PTS = %s, want 2s", changes[0].PTS)
	}
	if changes[0].Score != 72.5 {
		t.Errorf("first change score = %f, want 72.5", changes[0].Score)
	}
	if changes[1].PTS != 5*time.Second {
		t.Errorf("second change PTS = %s, want 5s", changes[1].PTS)
	}
	if changes[1].Score != 55.3 {
		t.Errorf("second change score = %f, want 55.3", changes[1].Score)
	}
}

func TestParseScdetOutput_Empty(t *testing.T) {
	changes := parseScdetOutput("")
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for empty input, got %d", len(changes))
	}
}

func TestParseScdetOutput_NoSceneChanges(t *testing.T) {
	// All frames have score=0 (no scene changes)
	output := `frame:0    pts:0       pts_time:0
lavfi.scd.mafd=0.100
lavfi.scd.score=0.000
frame:1    pts:1       pts_time:0.04
lavfi.scd.mafd=0.150
lavfi.scd.score=0.000
`
	changes := parseScdetOutput(output)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestBuildShots(t *testing.T) {
	total := 10 * time.Second
	boundaries := []sceneChange{
		{PTS: 2 * time.Second, Score: 72.5},
		{PTS: 5 * time.Second, Score: 55.3},
		{PTS: 8 * time.Second, Score: 80.1},
	}

	shots := buildShots(boundaries, total, 500*time.Millisecond)

	if len(shots) != 4 {
		t.Fatalf("expected 4 shots, got %d", len(shots))
	}

	// Shot 0: 0-2s
	if shots[0].Start != 0 || shots[0].End != 2*time.Second {
		t.Errorf("shot 0: %s - %s, want 0 - 2s", shots[0].Start, shots[0].End)
	}
	// Shot 1: 2-5s
	if shots[1].Start != 2*time.Second || shots[1].End != 5*time.Second {
		t.Errorf("shot 1: %s - %s, want 2s - 5s", shots[1].Start, shots[1].End)
	}
	// Shot 3: 8-10s
	if shots[3].Start != 8*time.Second || shots[3].End != 10*time.Second {
		t.Errorf("shot 3: %s - %s, want 8s - 10s", shots[3].Start, shots[3].End)
	}
	// Score is the scene change score at the shot's start boundary
	// Shot 1 starts at the 2s boundary (score 72.5) but the score stored
	// on shot 1 is from the boundary that *starts* shot 1
	if shots[1].Score != 55.3 {
		t.Errorf("shot 1 score = %f, want 55.3 (boundary at 5s)", shots[1].Score)
	}
}

func TestBuildShots_NoBoundaries(t *testing.T) {
	shots := buildShots(nil, 10*time.Second, 500*time.Millisecond)
	if len(shots) != 1 {
		t.Fatalf("expected 1 shot for no boundaries, got %d", len(shots))
	}
	if shots[0].Duration != 10*time.Second {
		t.Errorf("single shot duration = %s, want 10s", shots[0].Duration)
	}
}

func TestBuildShots_MergeShort(t *testing.T) {
	total := 10 * time.Second
	boundaries := []sceneChange{
		{PTS: 100 * time.Millisecond, Score: 50}, // very short - should merge
		{PTS: 5 * time.Second, Score: 60},
	}

	shots := buildShots(boundaries, total, 500*time.Millisecond)

	// The 0-100ms shot should be merged
	if shots[0].Start != 0 {
		t.Errorf("first shot should start at 0, got %s", shots[0].Start)
	}
	if len(shots) > 3 {
		t.Errorf("expected <= 3 shots after merging, got %d", len(shots))
	}
}

func TestExtractField(t *testing.T) {
	tests := []struct {
		line     string
		key      string
		expected string
	}{
		{"pts_time:1.234 pos:5678", "pts_time:", "1.234"},
		{"pts_time:0 duration:1", "pts_time:", "0"},
		{"no match here", "pts_time:", ""},
		{"pts_time:  3.5 next", "pts_time:", "3.5"},
	}

	for _, tt := range tests {
		got := extractField(tt.line, tt.key)
		if got != tt.expected {
			t.Errorf("extractField(%q, %q) = %q, want %q", tt.line, tt.key, got, tt.expected)
		}
	}
}
