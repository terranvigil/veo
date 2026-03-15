//go:build unit

package hull

import (
	"testing"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

func TestComputeUpper_SimpleCase(t *testing.T) {
	points := []Point{
		{Bitrate: 500, VMAF: 70, Resolution: ffmpeg.Res480p, CRF: 36},
		{Bitrate: 1000, VMAF: 80, Resolution: ffmpeg.Res480p, CRF: 30},
		{Bitrate: 1500, VMAF: 85, Resolution: ffmpeg.Res720p, CRF: 28},
		{Bitrate: 2000, VMAF: 90, Resolution: ffmpeg.Res720p, CRF: 24},
		{Bitrate: 3000, VMAF: 95, Resolution: ffmpeg.Res1080p, CRF: 22},
		// Dominated point: same bitrate as 720p@CRF28 but lower quality
		{Bitrate: 1500, VMAF: 78, Resolution: ffmpeg.Res1080p, CRF: 36},
	}

	h := ComputeUpper(points)

	// The dominated point (1500 kbps, VMAF 78) should be excluded
	for _, p := range h.Points {
		if p.Bitrate == 1500 && p.VMAF == 78 {
			t.Error("dominated point (1500, 78) should not be on the hull")
		}
	}

	// Hull should be monotonically increasing in both bitrate and quality
	for i := 1; i < len(h.Points); i++ {
		if h.Points[i].Bitrate < h.Points[i-1].Bitrate {
			t.Errorf("hull bitrate not monotonically increasing at index %d", i)
		}
		if h.Points[i].VMAF < h.Points[i-1].VMAF {
			t.Errorf("hull VMAF not monotonically increasing at index %d", i)
		}
	}
}

func TestComputeUpper_Empty(t *testing.T) {
	h := ComputeUpper(nil)
	if len(h.Points) != 0 {
		t.Error("hull of empty input should be empty")
	}
}

func TestComputeUpper_SinglePoint(t *testing.T) {
	points := []Point{
		{Bitrate: 1000, VMAF: 85},
	}
	h := ComputeUpper(points)
	if len(h.Points) != 1 {
		t.Errorf("hull of single point should have 1 point, got %d", len(h.Points))
	}
}

func TestComputeUpper_AllDominated(t *testing.T) {
	// Three points on a line - the middle one is on the hull boundary
	points := []Point{
		{Bitrate: 500, VMAF: 70},
		{Bitrate: 1000, VMAF: 80}, // on the line from (500,70) to (1500,90)
		{Bitrate: 1500, VMAF: 90},
	}
	h := ComputeUpper(points)
	// Collinear points may or may not be included depending on implementation
	// At minimum, endpoints should be on the hull
	if len(h.Points) < 2 {
		t.Errorf("hull should have at least 2 points, got %d", len(h.Points))
	}
	if h.Points[0].Bitrate != 500 || h.Points[len(h.Points)-1].Bitrate != 1500 {
		t.Error("hull should include the endpoints")
	}
}

func TestCrossovers(t *testing.T) {
	h := &Hull{
		Points: []Point{
			{Bitrate: 500, VMAF: 75, Resolution: ffmpeg.Res480p},
			{Bitrate: 1000, VMAF: 82, Resolution: ffmpeg.Res480p},
			{Bitrate: 1500, VMAF: 88, Resolution: ffmpeg.Res720p},
			{Bitrate: 3000, VMAF: 93, Resolution: ffmpeg.Res720p},
			{Bitrate: 5000, VMAF: 96, Resolution: ffmpeg.Res1080p},
		},
	}

	crossovers := h.Crossovers()
	if len(crossovers) != 2 {
		t.Fatalf("expected 2 crossovers, got %d", len(crossovers))
	}

	if crossovers[0].From != ffmpeg.Res480p || crossovers[0].To != ffmpeg.Res720p {
		t.Errorf("first crossover should be 480p→720p, got %s→%s",
			crossovers[0].From.Label(), crossovers[0].To.Label())
	}
	if crossovers[1].From != ffmpeg.Res720p || crossovers[1].To != ffmpeg.Res1080p {
		t.Errorf("second crossover should be 720p→1080p, got %s→%s",
			crossovers[1].From.Label(), crossovers[1].To.Label())
	}
}

func TestComputePerCodec(t *testing.T) {
	points := []Point{
		{Bitrate: 1000, VMAF: 80, Codec: ffmpeg.CodecX264},
		{Bitrate: 2000, VMAF: 90, Codec: ffmpeg.CodecX264},
		{Bitrate: 700, VMAF: 80, Codec: ffmpeg.CodecSVTAV1},
		{Bitrate: 1400, VMAF: 90, Codec: ffmpeg.CodecSVTAV1},
	}

	hulls := ComputePerCodec(points)

	if len(hulls) != 2 {
		t.Fatalf("expected 2 codec hulls, got %d", len(hulls))
	}
	if _, ok := hulls[ffmpeg.CodecX264]; !ok {
		t.Error("missing x264 hull")
	}
	if _, ok := hulls[ffmpeg.CodecSVTAV1]; !ok {
		t.Error("missing SVT-AV1 hull")
	}
}
