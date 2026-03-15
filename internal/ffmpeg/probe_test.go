//go:build unit

package ffmpeg

import (
	"testing"
)

func TestParseRational(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"25/1", 25.0},
		{"30000/1001", 29.97002997002997},
		{"50/1", 50.0},
		{"24000/1001", 23.976023976023978},
		{"0/0", 0.0},
		{"60", 60.0},
		{"", 0.0},
	}

	for _, tt := range tests {
		got := parseRational(tt.input)
		if got != tt.expected {
			// Allow small floating point tolerance
			diff := got - tt.expected
			if diff < -0.001 || diff > 0.001 {
				t.Errorf("parseRational(%q) = %f, want %f", tt.input, got, tt.expected)
			}
		}
	}
}

func TestConvertProbe(t *testing.T) {
	raw := &probeJSON{
		Format: probeFormatJSON{
			Filename:       "test.mp4",
			FormatName:     "mov,mp4,m4a,3gp,3g2,mj2",
			FormatLongName: "QuickTime / MOV",
			Duration:       "10.010000",
			Size:           "1234567",
			BitRate:        "987654",
			ProbeScore:     100,
		},
		Streams: []probeStreamJSON{
			{
				Index:            0,
				CodecName:        "h264",
				CodecType:        "video",
				Profile:          "High",
				Width:            1920,
				Height:           1080,
				PixFmt:           "yuv420p",
				RFrameRate:       "25/1",
				AvgFrameRate:     "25/1",
				BitRate:          "900000",
				NbFrames:         "250",
				BitsPerRawSample: "8",
			},
			{
				Index:         1,
				CodecName:     "aac",
				CodecType:     "audio",
				SampleRate:    "48000",
				Channels:      2,
				ChannelLayout: "stereo",
				BitRate:       "128000",
			},
		},
	}

	result := convertProbe(raw)

	// Format
	if result.Format.Filename != "test.mp4" {
		t.Errorf("filename = %q, want test.mp4", result.Format.Filename)
	}
	if result.Format.Duration != 10.01 {
		t.Errorf("duration = %f, want 10.01", result.Format.Duration)
	}
	if result.Format.Size != 1234567 {
		t.Errorf("size = %d, want 1234567", result.Format.Size)
	}
	if result.Format.BitRate != 987654 {
		t.Errorf("bitrate = %d, want 987654", result.Format.BitRate)
	}

	// Video stream
	vs := result.VideoStream()
	if vs == nil {
		t.Fatal("no video stream found")
	}
	if vs.Width != 1920 || vs.Height != 1080 {
		t.Errorf("resolution = %dx%d, want 1920x1080", vs.Width, vs.Height)
	}
	if vs.Resolution() != "1920x1080" {
		t.Errorf("Resolution() = %q, want 1920x1080", vs.Resolution())
	}
	if vs.FPS() != 25.0 {
		t.Errorf("FPS() = %f, want 25.0", vs.FPS())
	}
	if vs.NbFrames != 250 {
		t.Errorf("nb_frames = %d, want 250", vs.NbFrames)
	}
	if vs.BitsPerSample != 8 {
		t.Errorf("bits_per_sample = %d, want 8", vs.BitsPerSample)
	}

	// Audio stream
	as := result.AudioStream()
	if as == nil {
		t.Fatal("no audio stream found")
	}
	if as.SampleRate != 48000 {
		t.Errorf("sample_rate = %d, want 48000", as.SampleRate)
	}
	if as.Channels != 2 {
		t.Errorf("channels = %d, want 2", as.Channels)
	}
}

func TestResolutionLabel(t *testing.T) {
	tests := []struct {
		res      Resolution
		expected string
	}{
		{Res2160p, "2160p"},
		{Res1440p, "1440p"},
		{Res1080p, "1080p"},
		{Res720p, "720p"},
		{Res480p, "480p"},
		{Res360p, "360p"},
		{Res240p, "240p"},
		{Resolution{352, 288}, "240p"},
		{Resolution{176, 144}, "144p"},
	}

	for _, tt := range tests {
		got := tt.res.Label()
		if got != tt.expected {
			t.Errorf("%s.Label() = %q, want %q", tt.res.String(), got, tt.expected)
		}
	}
}

func TestStreamValidate(t *testing.T) {
	// Valid video stream
	s := &StreamInfo{CodecType: "video", Width: 1920, Height: 1080}
	if err := s.Validate(); err != nil {
		t.Errorf("valid stream should pass: %v", err)
	}

	// Zero dimensions
	s = &StreamInfo{CodecType: "video", Width: 0, Height: 1080}
	if err := s.Validate(); err == nil {
		t.Error("zero width should fail validation")
	}

	// Audio stream
	s = &StreamInfo{CodecType: "audio"}
	if err := s.Validate(); err == nil {
		t.Error("audio stream should fail video validation")
	}
}

func TestVideoStreamNil(t *testing.T) {
	result := &ProbeResult{
		Streams: []StreamInfo{
			{CodecType: "audio"},
		},
	}
	if result.VideoStream() != nil {
		t.Error("expected nil video stream")
	}
}
