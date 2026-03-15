package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/quality"
)

var qualityCmd = &cobra.Command{
	Use:   "quality",
	Short: "Quality measurement tools",
}

var (
	qualityRef       string
	qualityDist      string
	qualitySubsample int
	qualityModel     string
	qualityPerFrame  bool
	qualityOutput    string
)

var qualityMeasureCmd = &cobra.Command{
	Use:   "measure",
	Short: "Measure quality (VMAF/PSNR/SSIM) between reference and distorted video",
	RunE:  runQualityMeasure,
}

func init() {
	qualityMeasureCmd.Flags().StringVar(&qualityRef, "reference", "", "reference (original) video file")
	qualityMeasureCmd.Flags().StringVar(&qualityDist, "distorted", "", "distorted (encoded) video file")
	qualityMeasureCmd.Flags().IntVar(&qualitySubsample, "subsample", 0, "VMAF frame subsampling (0 = every frame)")
	qualityMeasureCmd.Flags().StringVar(&qualityModel, "model", "vmaf_v0.6.1", "VMAF model name")
	qualityMeasureCmd.Flags().BoolVar(&qualityPerFrame, "per-frame", false, "include per-frame metrics")
	qualityMeasureCmd.Flags().StringVarP(&qualityOutput, "output", "o", "", "save results as JSON (required for comparison player)")

	mustMarkRequired(qualityMeasureCmd, "reference")
	mustMarkRequired(qualityMeasureCmd, "distorted")

	qualityCmd.AddCommand(qualityMeasureCmd)
	rootCmd.AddCommand(qualityCmd)
}

func runQualityMeasure(cmd *cobra.Command, args []string) error {
	opts := quality.MeasureOpts{
		Metrics:   []quality.Metric{quality.MetricVMAF, quality.MetricPSNR, quality.MetricSSIM},
		Subsample: qualitySubsample,
		Model:     qualityModel,
		PerFrame:  qualityPerFrame,
	}

	result, err := quality.Measure(context.Background(), qualityRef, qualityDist, opts)
	if err != nil {
		return fmt.Errorf("quality measurement failed: %w", err)
	}

	fmt.Printf("VMAF:  %.2f\n", result.VMAF)
	fmt.Printf("PSNR:  %.2f dB\n", result.PSNR)
	fmt.Printf("SSIM:  %.6f\n", result.SSIM)

	if qualityPerFrame && len(result.Frames) > 0 {
		fmt.Printf("\nPer-frame: %d frames measured\n", len(result.Frames))
		fmt.Printf("%-8s  %-8s  %-10s  %-10s\n", "Frame", "VMAF", "PSNR", "SSIM")
		limit := len(result.Frames)
		if limit > 20 {
			limit = 20
		}
		for _, f := range result.Frames[:limit] {
			fmt.Printf("%-8d  %-8.2f  %-10.2f  %-10.6f\n", f.FrameNum, f.VMAF, f.PSNR, f.SSIM)
		}
		if len(result.Frames) > 20 {
			fmt.Printf("... (%d more frames)\n", len(result.Frames)-20)
		}
	}

	// lSave to JSON if requested
	if qualityOutput != "" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal results: %w", err)
		}
		if err := os.WriteFile(qualityOutput, data, 0o644); err != nil {
			return fmt.Errorf("failed to write results: %w", err)
		}
		fmt.Printf("\nResults saved to: %s\n", qualityOutput)
	}

	return nil
}
