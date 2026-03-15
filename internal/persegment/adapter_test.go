//go:build unit

package persegment

import "testing"

func TestComplexityToCRF(t *testing.T) {
	tests := []struct {
		score    float64
		minCRF   int
		maxCRF   int
		expected int
	}{
		{0, 15, 45, 45},   // zero complexity → max CRF
		{100, 15, 45, 15}, // max complexity → min CRF
		{50, 15, 45, 30},  // mid complexity → mid CRF
		{25, 15, 45, 38},  // low complexity → high CRF
		{75, 15, 45, 23},  // high complexity → low CRF
		{0, 20, 40, 40},   // different range
		{100, 20, 40, 20}, // different range
	}

	for _, tt := range tests {
		got := complexityToCRF(tt.score, tt.minCRF, tt.maxCRF)
		if got != tt.expected {
			t.Errorf("complexityToCRF(%.0f, %d, %d) = %d, want %d",
				tt.score, tt.minCRF, tt.maxCRF, got, tt.expected)
		}
	}
}

func TestComplexityToCRF_Monotonic(t *testing.T) {
	// Higher complexity should always produce lower or equal CRF
	prevCRF := 100
	for score := 0.0; score <= 100; score += 5 {
		crf := complexityToCRF(score, 15, 45)
		if crf > prevCRF {
			t.Errorf("CRF increased from %d to %d at score %.0f - should be monotonic",
				prevCRF, crf, score)
		}
		prevCRF = crf
	}
}
