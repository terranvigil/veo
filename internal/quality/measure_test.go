//go:build unit

package quality

import (
	"testing"
)

func TestParseVMAFLog(t *testing.T) {
	// Sample libvmaf JSON output (abbreviated)
	logJSON := `{
		"version": "vmaf v3.0.0",
		"frames": [
			{
				"frameNum": 0,
				"metrics": {
					"vmaf": 92.5,
					"psnr_y": 38.2,
					"float_ssim": 0.987
				}
			},
			{
				"frameNum": 1,
				"metrics": {
					"vmaf": 94.1,
					"psnr_y": 39.5,
					"float_ssim": 0.991
				}
			},
			{
				"frameNum": 2,
				"metrics": {
					"vmaf": 90.3,
					"psnr_y": 37.1,
					"float_ssim": 0.982
				}
			}
		],
		"pooled_metrics": {
			"vmaf": {
				"mean": 92.3,
				"min": 90.3,
				"max": 94.1
			},
			"psnr_y": {
				"mean": 38.27,
				"min": 37.1,
				"max": 39.5
			},
			"float_ssim": {
				"mean": 0.9867,
				"min": 0.982,
				"max": 0.991
			}
		}
	}`

	// Without per-frame
	result, err := parseVMAFLog([]byte(logJSON), false)
	if err != nil {
		t.Fatalf("parseVMAFLog failed: %v", err)
	}

	if result.VMAF != 92.3 {
		t.Errorf("VMAF = %f, want 92.3", result.VMAF)
	}
	if result.PSNR != 38.27 {
		t.Errorf("PSNR = %f, want 38.27", result.PSNR)
	}
	if result.SSIM != 0.9867 {
		t.Errorf("SSIM = %f, want 0.9867", result.SSIM)
	}
	if len(result.Frames) != 0 {
		t.Errorf("expected no per-frame data, got %d frames", len(result.Frames))
	}

	// With per-frame
	result, err = parseVMAFLog([]byte(logJSON), true)
	if err != nil {
		t.Fatalf("parseVMAFLog with per-frame failed: %v", err)
	}

	if len(result.Frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(result.Frames))
	}
	if result.Frames[0].VMAF != 92.5 {
		t.Errorf("frame 0 VMAF = %f, want 92.5", result.Frames[0].VMAF)
	}
	if result.Frames[1].PSNR != 39.5 {
		t.Errorf("frame 1 PSNR = %f, want 39.5", result.Frames[1].PSNR)
	}
	if result.Frames[2].SSIM != 0.982 {
		t.Errorf("frame 2 SSIM = %f, want 0.982", result.Frames[2].SSIM)
	}
}

func TestParseVMAFLog_VMAFOnly(t *testing.T) {
	// libvmaf output when only VMAF is requested (no psnr/ssim features)
	logJSON := `{
		"frames": [
			{"frameNum": 0, "metrics": {"vmaf": 85.0}}
		],
		"pooled_metrics": {
			"vmaf": {"mean": 85.0, "min": 85.0, "max": 85.0}
		}
	}`

	result, err := parseVMAFLog([]byte(logJSON), false)
	if err != nil {
		t.Fatalf("parseVMAFLog failed: %v", err)
	}
	if result.VMAF != 85.0 {
		t.Errorf("VMAF = %f, want 85.0", result.VMAF)
	}
	if result.PSNR != 0 {
		t.Errorf("PSNR = %f, want 0 (not requested)", result.PSNR)
	}
}

func TestParseVMAFLog_InvalidJSON(t *testing.T) {
	_, err := parseVMAFLog([]byte("not json"), false)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
