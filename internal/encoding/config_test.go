//go:build unit

package encoding

import (
	"runtime"
	"testing"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

func TestPresetForCodec(t *testing.T) {
	tests := []struct {
		codec    ffmpeg.Codec
		input    string
		expected string
	}{
		{ffmpeg.CodecX264, "veryfast", "veryfast"},
		{ffmpeg.CodecX264, "medium", "medium"},
		{ffmpeg.CodecX264, "slow", "slow"},
		{ffmpeg.CodecX265, "veryfast", "veryfast"},
		{ffmpeg.CodecSVTAV1, "ultrafast", "12"},
		{ffmpeg.CodecSVTAV1, "veryfast", "10"},
		{ffmpeg.CodecSVTAV1, "fast", "8"},
		{ffmpeg.CodecSVTAV1, "medium", "6"},
		{ffmpeg.CodecSVTAV1, "slow", "4"},
		{ffmpeg.CodecSVTAV1, "veryslow", "0"},
		{ffmpeg.CodecSVTAV1, "8", "8"},
	}

	for _, tt := range tests {
		got := PresetForCodec(tt.codec, tt.input)
		if got != tt.expected {
			t.Errorf("PresetForCodec(%s, %q) = %q, want %q", tt.codec, tt.input, got, tt.expected)
		}
	}
}

func TestValidate(t *testing.T) {
	// Valid config
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}

	// Empty resolutions
	c = DefaultConfig()
	c.Resolutions = nil
	if err := c.Validate(); err == nil {
		t.Error("empty resolutions should fail validation")
	}

	// Empty CRF values
	c = DefaultConfig()
	c.CRFValues = nil
	if err := c.Validate(); err == nil {
		t.Error("empty CRF values should fail validation")
	}

	// Empty codecs
	c = DefaultConfig()
	c.Codecs = nil
	if err := c.Validate(); err == nil {
		t.Error("empty codecs should fail validation")
	}

	// Unknown codec
	c = DefaultConfig()
	c.Codecs = []ffmpeg.Codec{"libunknown"}
	if err := c.Validate(); err == nil {
		t.Error("unknown codec should fail validation")
	}

	// Negative subsample
	c = DefaultConfig()
	c.Subsample = -1
	if err := c.Validate(); err == nil {
		t.Error("negative subsample should fail validation")
	}
}

func TestEffectiveParallel(t *testing.T) {
	// Explicit value
	c := Config{Parallel: 4}
	if c.EffectiveParallel() != 4 {
		t.Errorf("explicit parallel 4, got %d", c.EffectiveParallel())
	}

	// Auto (0) should use NumCPU/2, floor 2
	c = Config{Parallel: 0}
	expected := runtime.NumCPU() / 2
	if expected < 2 {
		expected = 2
	}
	if c.EffectiveParallel() != expected {
		t.Errorf("auto parallel: expected %d, got %d", expected, c.EffectiveParallel())
	}
}
