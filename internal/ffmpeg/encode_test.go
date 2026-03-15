//go:build unit

package ffmpeg

import "testing"

func TestParseProgressLine(t *testing.T) {
	var p Progress

	// Frame
	if parseProgressLine("frame=150", &p) {
		t.Error("should not signal complete on frame=")
	}
	if p.Frame != 150 {
		t.Errorf("frame = %d, want 150", p.Frame)
	}

	// FPS
	parseProgressLine("fps=30.5", &p)
	if p.FPS != 30.5 {
		t.Errorf("fps = %f, want 30.5", p.FPS)
	}

	// Bitrate
	parseProgressLine("bitrate=1234.5kbits/s", &p)
	if p.Bitrate != 1234.5 {
		t.Errorf("bitrate = %f, want 1234.5", p.Bitrate)
	}

	// Speed
	parseProgressLine("speed=2.5x", &p)
	if p.Speed != 2.5 {
		t.Errorf("speed = %f, want 2.5", p.Speed)
	}

	// out_time_us
	parseProgressLine("out_time_us=10010000", &p)
	if p.Time.Seconds() < 10.0 || p.Time.Seconds() > 10.02 {
		t.Errorf("time = %s, want ~10s", p.Time)
	}

	// progress=continue signals complete block
	if !parseProgressLine("progress=continue", &p) {
		t.Error("progress=continue should signal complete")
	}

	// Invalid line
	if parseProgressLine("garbage", &p) {
		t.Error("garbage line should not signal complete")
	}
}

func TestBuildEncodeArgs(t *testing.T) {
	job := EncodeJob{
		Input:      "input.y4m",
		Output:     "output.mp4",
		Codec:      CodecX264,
		CRF:        23,
		Preset:     "medium",
		Resolution: Res720p,
	}

	args := buildEncodeArgs(job)

	// Check essential args are present
	hasArg := func(target string) bool {
		for _, a := range args {
			if a == target {
				return true
			}
		}
		return false
	}

	if !hasArg("-y") {
		t.Error("missing -y (overwrite)")
	}
	if !hasArg("input.y4m") {
		t.Error("missing input file")
	}
	if !hasArg("output.mp4") {
		t.Error("missing output file")
	}
	if !hasArg("libx264") {
		t.Error("missing codec")
	}
	if !hasArg("23") {
		t.Error("missing CRF value")
	}
	if !hasArg("medium") {
		t.Error("missing preset")
	}
	if !hasArg("-an") {
		t.Error("missing -an (no audio)")
	}
}

func TestBuildEncodeArgs_NoResolution(t *testing.T) {
	job := EncodeJob{
		Input:  "input.y4m",
		Output: "output.mp4",
		Codec:  CodecSVTAV1,
		CRF:    30,
	}

	args := buildEncodeArgs(job)

	// Should not have a scale filter when no resolution specified
	for _, a := range args {
		if a == "-vf" {
			t.Error("should not have -vf when no resolution specified")
		}
	}
}
