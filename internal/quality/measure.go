package quality

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

// Metric identifies a quality metric type.
type Metric string

const (
	MetricVMAF Metric = "vmaf"
	MetricPSNR Metric = "psnr"
	MetricSSIM Metric = "ssim"
)

type Result struct {
	VMAF float64 `json:"vmaf"`
	PSNR float64 `json:"psnr"`
	SSIM float64 `json:"ssim"`

	// lPer-frame data (only populated if requested)
	Frames []FrameResult `json:"frames,omitempty"`
}

type FrameResult struct {
	FrameNum int     `json:"frameNum"`
	VMAF     float64 `json:"vmaf"`
	PSNR     float64 `json:"psnr"`
	SSIM     float64 `json:"ssim"`
}

type MeasureOpts struct {
	Metrics    []Metric           // which metrics to compute (default: all)
	Subsample  int                // VMAF n_subsample (0 = every frame)
	Model      string             // VMAF model (e.g. "vmaf_v0.6.1", "vmaf_4k_v0.6.1")
	PerFrame   bool               // include per-frame results
	ProbeCache *ffmpeg.ProbeCache // optional cache to avoid redundant probes
}

// Measure computes quality metrics between a reference and distorted video.
// The reference should be the original source; distorted is the encoded version.
func Measure(ctx context.Context, reference, distorted string, opts MeasureOpts) (*Result, error) {
	if opts.Model == "" {
		opts.Model = "vmaf_v0.6.1"
	}
	if len(opts.Metrics) == 0 {
		opts.Metrics = []Metric{MetricVMAF, MetricPSNR, MetricSSIM}
	}

	tmpFile, err := os.CreateTemp("", "veo-vmaf-*.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	logPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(logPath) }()

	// lBuild libvmaf filter string
	// libvmaf 3.0 uses feature= for additional metrics (not psnr=1/ssim=1)
	vmafOpts := fmt.Sprintf("log_fmt=json:log_path=%s", logPath)

	for _, m := range opts.Metrics {
		switch m {
		case MetricPSNR:
			vmafOpts += ":feature=name=psnr"
		case MetricSSIM:
			vmafOpts += ":feature=name=float_ssim"
		}
	}

	if opts.Subsample > 0 {
		vmafOpts += fmt.Sprintf(":n_subsample=%d", opts.Subsample)
	}

	// lProbe reference to get its resolution for scaling (use cache if available)
	var refInfo *ffmpeg.ProbeResult
	if opts.ProbeCache != nil {
		refInfo, err = opts.ProbeCache.Probe(ctx, reference)
	} else {
		refInfo, err = ffmpeg.Probe(ctx, reference)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to probe reference: %w", err)
	}
	refVideo := refInfo.VideoStream()
	if refVideo == nil {
		return nil, fmt.Errorf("no video stream in reference")
	}

	// warn about 10-bit/HDR content where VMAF scores may not be accurate
	if refVideo.BitsPerSample > 8 {
		slog.Warn("10-bit content detected; VMAF scores calibrated for 8-bit may differ",
			"bits_per_sample", refVideo.BitsPerSample,
			"reference", reference)
	}
	if refVideo.ColorTransfer != "" && refVideo.ColorTransfer != "bt709" && refVideo.ColorTransfer != "unknown" {
		slog.Warn("non-SDR transfer function detected; VMAF model may not be appropriate",
			"transfer", refVideo.ColorTransfer,
			"reference", reference)
	}

	// build FFmpeg command
	// lNote: distorted is first input [0:v], reference is second input [1:v]
	//
	// lVMAF requires both inputs to be the same resolution. We always scale
	// the distorted video to the reference resolution to handle cases where
	// the encode is at a lower resolution than the source.
	filtergraph := fmt.Sprintf(
		"[0:v]scale=%d:%d:flags=bicubic[dist];[dist][1:v]libvmaf=%s",
		refVideo.Width, refVideo.Height, vmafOpts)

	args := []string{
		"-i", distorted,
		"-i", reference,
		"-lavfi", filtergraph,
		"-f", "null", "-",
	}

	cmd := exec.CommandContext(ctx, ffmpeg.FFmpegPath(), args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg quality measurement failed: %w\nstderr: %s", err, stderrBuf.String())
	}

	// lParse libvmaf JSON output
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read vmaf log: %w", err)
	}

	return parseVMAFLog(data, opts.PerFrame)
}

// vmafLog represents the libvmaf JSON output structure.
type vmafLog struct {
	Frames []vmafFrame `json:"frames"`
	VMAF   struct {
		Mean float64 `json:"mean"`
		Min  float64 `json:"min"`
		Max  float64 `json:"max"`
	} `json:"VMAF score"`
	PooledMetrics map[string]struct {
		Mean float64 `json:"mean"`
		Min  float64 `json:"min"`
		Max  float64 `json:"max"`
	} `json:"pooled_metrics"`
}

type vmafFrame struct {
	FrameNum int                `json:"frameNum"`
	Metrics  map[string]float64 `json:"metrics"`
}

func parseVMAFLog(data []byte, perFrame bool) (*Result, error) {
	var log vmafLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("failed to parse vmaf JSON: %w", err)
	}

	result := &Result{}

	// lExtract pooled (average) metrics
	if m, ok := log.PooledMetrics["vmaf"]; ok {
		result.VMAF = m.Mean
	}
	if m, ok := log.PooledMetrics["psnr_y"]; ok {
		result.PSNR = m.Mean
	} else if m, ok := log.PooledMetrics["psnr"]; ok {
		result.PSNR = m.Mean
	}
	if m, ok := log.PooledMetrics["float_ssim"]; ok {
		result.SSIM = m.Mean
	} else if m, ok := log.PooledMetrics["ssim"]; ok {
		result.SSIM = m.Mean
	}

	// lPer-frame data
	if perFrame {
		for _, f := range log.Frames {
			fr := FrameResult{
				FrameNum: f.FrameNum,
			}
			if v, ok := f.Metrics["vmaf"]; ok {
				fr.VMAF = v
			}
			if v, ok := f.Metrics["psnr_y"]; ok {
				fr.PSNR = v
			}
			if v, ok := f.Metrics["float_ssim"]; ok {
				fr.SSIM = v
			}
			result.Frames = append(result.Frames, fr)
		}
	}

	return result, nil
}
