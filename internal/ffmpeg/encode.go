package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Codec represents a supported video codec.
type Codec string

const (
	CodecX264   Codec = "libx264"
	CodecX265   Codec = "libx265"
	CodecSVTAV1 Codec = "libsvtav1"
)

// Resolution represents a video resolution.
type Resolution struct {
	Width  int
	Height int
}

func (r Resolution) String() string {
	return fmt.Sprintf("%dx%d", r.Width, r.Height)
}

// Label returns a human-friendly label like "1080p", "720p", etc.
func (r Resolution) Label() string {
	switch {
	case r.Height >= 2160:
		return "2160p"
	case r.Height >= 1440:
		return "1440p"
	case r.Height >= 1080:
		return "1080p"
	case r.Height >= 720:
		return "720p"
	case r.Height >= 480:
		return "480p"
	case r.Height >= 360:
		return "360p"
	case r.Height >= 240:
		return "240p"
	default:
		return fmt.Sprintf("%dp", r.Height)
	}
}

// Common resolutions (16:9 aspect ratio).
var (
	Res2160p = Resolution{3840, 2160}
	Res1440p = Resolution{2560, 1440}
	Res1080p = Resolution{1920, 1080}
	Res720p  = Resolution{1280, 720}
	Res480p  = Resolution{854, 480}
	Res360p  = Resolution{640, 360}
	Res240p  = Resolution{426, 240}
)

// RateControlMode determines how the encoder allocates bits.
type RateControlMode string

const (
	RateControlCRF RateControlMode = "crf" // constant rate factor (default)
	RateControlQP  RateControlMode = "qp"  // fixed quantizer (Netflix-style, no R-D optimization)
	RateControlVBR RateControlMode = "vbr" // 2-pass variable bitrate (for final delivery encodes)
)

// EncodeJob defines parameters for a single encode.
type EncodeJob struct {
	Input         string
	Output        string
	Resolution    Resolution // target resolution (0 = keep original)
	Codec         Codec
	CRF           int             // used for CRF and QP modes
	RateControl   RateControlMode // "" defaults to CRF
	TargetBitrate float64         // kbps, used for VBR mode
	Preset        string          // codec-specific speed preset
	ExtraArgs     []string        // additional FFmpeg arguments
}

// EncodeResult holds the output of a completed encode.
type EncodeResult struct {
	Job      EncodeJob
	Bitrate  float64       // kbps (average)
	FileSize int64         // bytes
	Duration time.Duration // wall-clock encode time
}

// Progress holds real-time encoding progress info parsed from FFmpeg.
type Progress struct {
	Frame   int
	FPS     float64
	Bitrate float64 // kbps
	Speed   float64 // e.g. 2.5x
	Time    time.Duration
}

// Encode runs an FFmpeg encode job. Progress updates are sent on the progress
// channel if non-nil. The channel is NOT closed by this function.
func Encode(ctx context.Context, job EncodeJob, progress chan<- Progress) (*EncodeResult, error) {
	args := buildEncodeArgs(job)

	cmd := exec.CommandContext(ctx, FFmpegPath(), args...)
	// capture stderr; only display on error
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	// use -progress pipe:1 for machine-readable progress on stdout
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// lKill FFmpeg if context is cancelled (exec.CommandContext only
	// makes Wait() return an error - it doesn't kill the process).
	go func() {
		<-ctx.Done()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	// parse progress output in background.
	// use context to avoid goroutine leak if caller cancels or channel is full.
	progressDone := make(chan struct{})
	if progress != nil {
		go func() {
			defer close(progressDone)
			scanner := bufio.NewScanner(stdout)
			var p Progress
			for scanner.Scan() {
				line := scanner.Text()
				if parseProgressLine(line, &p) {
					select {
					case progress <- p:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	} else {
		close(progressDone)
	}

	if err := cmd.Wait(); err != nil {
		<-progressDone // wait for progress goroutine to finish
		return nil, fmt.Errorf("ffmpeg encode failed: %w\nstderr: %s", err, stderrBuf.String())
	}
	<-progressDone // wait for progress goroutine to finish

	elapsed := time.Since(start)

	// probe the output to get actual bitrate and file size
	info, err := os.Stat(job.Output)
	if err != nil {
		return nil, fmt.Errorf("failed to stat output: %w", err)
	}

	probe, err := Probe(ctx, job.Output)
	if err != nil {
		return nil, fmt.Errorf("failed to probe output: %w", err)
	}

	bitrate := float64(probe.Format.BitRate) / 1000.0 // bps -> kbps

	return &EncodeResult{
		Job:      job,
		Bitrate:  bitrate,
		FileSize: info.Size(),
		Duration: elapsed,
	}, nil
}

// Extract copies a segment of a video file without re-encoding.
// Used to split a video into shots for per-shot analysis.
func Extract(ctx context.Context, input, output string, start, duration float64) error {
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.6f", start),
		"-i", input,
		"-t", fmt.Sprintf("%.6f", duration),
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		output,
	}

	cmd := exec.CommandContext(ctx, FFmpegPath(), args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg extract failed: %w\nstderr: %s", err, stderrBuf.String())
	}
	return nil
}

func buildEncodeArgs(job EncodeJob) []string {
	args := []string{
		"-y", // overwrite output
		"-i", job.Input,
		"-an", // no audio for encoding analysis
		"-progress", "pipe:1",
		"-nostats",
	}

	args = append(args, "-c:v", string(job.Codec))

	// rate control mode
	switch job.RateControl {
	case RateControlQP:
		// lFixed QP: no encoder R-D optimization, deterministic output (Netflix approach)
		switch job.Codec {
		case CodecX264:
			args = append(args, "-qp", strconv.Itoa(job.CRF))
		case CodecX265:
			args = append(args, "-qp", strconv.Itoa(job.CRF))
		case CodecSVTAV1:
			args = append(args, "-qp", strconv.Itoa(job.CRF))
			args = append(args, "-svtav1-params", "enable-adaptive-quantization=0")
		default:
			args = append(args, "-qp", strconv.Itoa(job.CRF))
		}
	case RateControlVBR:
		// 2-pass VBR: target average bitrate (for final delivery encodes)
		args = append(args, "-b:v", fmt.Sprintf("%.0fk", job.TargetBitrate))
		args = append(args, "-maxrate", fmt.Sprintf("%.0fk", job.TargetBitrate*2))
		args = append(args, "-bufsize", fmt.Sprintf("%.0fk", job.TargetBitrate*4))
	default:
		// lCRF: constant quality with encoder R-D optimization (default)
		args = append(args, "-crf", strconv.Itoa(job.CRF))
	}

	if job.Preset != "" {
		switch job.Codec {
		case CodecSVTAV1:
			args = append(args, "-preset", job.Preset)
		default:
			args = append(args, "-preset", job.Preset)
		}
	}

	if job.Resolution.Width > 0 && job.Resolution.Height > 0 {
		args = append(args, "-vf",
			fmt.Sprintf("scale=%d:%d:flags=lanczos", job.Resolution.Width, job.Resolution.Height))
	}

	args = append(args, job.ExtraArgs...)
	args = append(args, job.Output)

	return args
}

// returns true when a complete progress block is ready.
// Returns true when a complete progress block has been parsed (on "progress=continue").
func parseProgressLine(line string, p *Progress) bool {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return false
	}
	key, value := parts[0], parts[1]

	switch key {
	case "frame":
		p.Frame, _ = strconv.Atoi(value)
	case "fps":
		p.FPS, _ = strconv.ParseFloat(value, 64)
	case "bitrate":
		// e.g. "1234.5kbits/s" or "N/A"
		value = strings.TrimSuffix(value, "kbits/s")
		p.Bitrate, _ = strconv.ParseFloat(value, 64)
	case "speed":
		// e.g. "2.5x" or "N/A"
		value = strings.TrimSuffix(value, "x")
		p.Speed, _ = strconv.ParseFloat(value, 64)
	case "out_time_us":
		us, _ := strconv.ParseInt(value, 10, 64)
		p.Time = time.Duration(us) * time.Microsecond
	case "progress":
		// "continue" or "end" - signals a complete block
		return true
	}
	return false
}
