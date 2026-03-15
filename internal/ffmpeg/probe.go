package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

type ProbeResult struct {
	Format  FormatInfo   `json:"format"`
	Streams []StreamInfo `json:"streams"`
}

type FormatInfo struct {
	Filename       string  `json:"filename"`
	FormatName     string  `json:"format_name"`
	FormatLongName string  `json:"format_long_name"`
	Duration       float64 `json:"duration"` // seconds
	Size           int64   `json:"size"`     // bytes
	BitRate        int64   `json:"bit_rate"` // bits/sec
	ProbeScore     int     `json:"probe_score"`
}

type StreamInfo struct {
	Index          int     `json:"index"`
	CodecName      string  `json:"codec_name"`
	CodecLongName  string  `json:"codec_long_name"`
	CodecType      string  `json:"codec_type"` // "video", "audio", "subtitle"
	Profile        string  `json:"profile"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	PixFmt         string  `json:"pix_fmt"`
	Level          int     `json:"level"`
	FieldOrder     string  `json:"field_order"`
	ColorRange     string  `json:"color_range"`
	ColorSpace     string  `json:"color_space"`
	ColorTransfer  string  `json:"color_transfer"`
	ColorPrimaries string  `json:"color_primaries"`
	Duration       float64 `json:"duration"` // seconds
	BitRate        int64   `json:"bit_rate"` // bits/sec
	NbFrames       int     `json:"nb_frames"`
	RFrameRate     string  `json:"r_frame_rate"`   // e.g. "25/1"
	AvgFrameRate   string  `json:"avg_frame_rate"` // e.g. "25/1"
	SampleRate     int     `json:"sample_rate"`    // audio
	Channels       int     `json:"channels"`       // audio
	ChannelLayout  string  `json:"channel_layout"` // audio
	BitsPerSample  int     `json:"bits_per_raw_sample"`
}

// Validate checks that the video stream has sane metadata.
func (s *StreamInfo) Validate() error {
	if s.CodecType != "video" {
		return fmt.Errorf("not a video stream (type=%s)", s.CodecType)
	}
	if s.Width <= 0 || s.Height <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", s.Width, s.Height)
	}
	return nil
}

// FPS returns the frame rate as a float64 parsed from r_frame_rate (e.g. "50/1" -> 50.0).
func (s *StreamInfo) FPS() float64 {
	return parseRational(s.RFrameRate)
}

// Resolution returns "WxH" string.
func (s *StreamInfo) Resolution() string {
	if s.Width == 0 || s.Height == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", s.Width, s.Height)
}

// DurationTime returns the format duration as time.Duration.
func (f *FormatInfo) DurationTime() time.Duration {
	return time.Duration(f.Duration * float64(time.Second))
}

// VideoStream returns the first video stream, or nil if none found.
func (p *ProbeResult) VideoStream() *StreamInfo {
	for i := range p.Streams {
		if p.Streams[i].CodecType == "video" {
			return &p.Streams[i]
		}
	}
	return nil
}

// AudioStream returns the first audio stream, or nil if none found.
func (p *ProbeResult) AudioStream() *StreamInfo {
	for i := range p.Streams {
		if p.Streams[i].CodecType == "audio" {
			return &p.Streams[i]
		}
	}
	return nil
}

// probeJSON is the raw ffprobe JSON output structure.
// ffprobe outputs numbers as strings in JSON, so we unmarshal to this
// intermediate type and then convert.
type probeJSON struct {
	Format  probeFormatJSON   `json:"format"`
	Streams []probeStreamJSON `json:"streams"`
}

type probeFormatJSON struct {
	Filename       string `json:"filename"`
	FormatName     string `json:"format_name"`
	FormatLongName string `json:"format_long_name"`
	Duration       string `json:"duration"`
	Size           string `json:"size"`
	BitRate        string `json:"bit_rate"`
	ProbeScore     int    `json:"probe_score"`
}

type probeStreamJSON struct {
	Index            int    `json:"index"`
	CodecName        string `json:"codec_name"`
	CodecLongName    string `json:"codec_long_name"`
	CodecType        string `json:"codec_type"`
	Profile          string `json:"profile"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	PixFmt           string `json:"pix_fmt"`
	Level            int    `json:"level"`
	FieldOrder       string `json:"field_order"`
	ColorRange       string `json:"color_range"`
	ColorSpace       string `json:"color_space"`
	ColorTransfer    string `json:"color_transfer"`
	ColorPrimaries   string `json:"color_primaries"`
	Duration         string `json:"duration"`
	BitRate          string `json:"bit_rate"`
	NbFrames         string `json:"nb_frames"`
	RFrameRate       string `json:"r_frame_rate"`
	AvgFrameRate     string `json:"avg_frame_rate"`
	SampleRate       string `json:"sample_rate"`
	Channels         int    `json:"channels"`
	ChannelLayout    string `json:"channel_layout"`
	BitsPerRawSample string `json:"bits_per_raw_sample"`
}

// Probe runs ffprobe on the given file and returns parsed results.
func Probe(ctx context.Context, path string) (*ProbeResult, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}

	cmd := exec.CommandContext(ctx, FFprobePath(), args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed for %s: %w\nstderr: %s", path, err, stderrBuf.String())
	}

	var raw probeJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	return convertProbe(&raw), nil
}

func convertProbe(raw *probeJSON) *ProbeResult {
	result := &ProbeResult{
		Format: FormatInfo{
			Filename:       raw.Format.Filename,
			FormatName:     raw.Format.FormatName,
			FormatLongName: raw.Format.FormatLongName,
			Duration:       parseFloat(raw.Format.Duration),
			Size:           parseInt64(raw.Format.Size),
			BitRate:        parseInt64(raw.Format.BitRate),
			ProbeScore:     raw.Format.ProbeScore,
		},
	}

	for _, s := range raw.Streams {
		result.Streams = append(result.Streams, StreamInfo{
			Index:          s.Index,
			CodecName:      s.CodecName,
			CodecLongName:  s.CodecLongName,
			CodecType:      s.CodecType,
			Profile:        s.Profile,
			Width:          s.Width,
			Height:         s.Height,
			PixFmt:         s.PixFmt,
			Level:          s.Level,
			FieldOrder:     s.FieldOrder,
			ColorRange:     s.ColorRange,
			ColorSpace:     s.ColorSpace,
			ColorTransfer:  s.ColorTransfer,
			ColorPrimaries: s.ColorPrimaries,
			Duration:       parseFloat(s.Duration),
			BitRate:        parseInt64(s.BitRate),
			NbFrames:       parseInt(s.NbFrames),
			RFrameRate:     s.RFrameRate,
			AvgFrameRate:   s.AvgFrameRate,
			SampleRate:     parseInt(s.SampleRate),
			Channels:       s.Channels,
			ChannelLayout:  s.ChannelLayout,
			BitsPerSample:  parseInt(s.BitsPerRawSample),
		})
	}

	return result
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func parseRational(s string) float64 {
	var num, den int
	if _, err := fmt.Sscanf(s, "%d/%d", &num, &den); err == nil && den != 0 {
		return float64(num) / float64(den)
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
