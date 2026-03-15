package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/persegment"
)

var persegmentCmd = &cobra.Command{
	Use:     "per-segment",
	Aliases: []string{"per-frame"}, // backward compat
	Short:   "Segment-level adaptive CRF encoding",
}

var (
	pfInput      string
	pfCodec      string
	pfPreset     string
	pfTargetVMAF float64
	pfTolerance  float64
	pfMinCRF     int
	pfMaxCRF     int
	pfMaxIter    int
)

var persegmentAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Run segment-level adaptive CRF analysis",
	Long: `Analyzes content complexity per 2-second segment, assigns CRF values
based on complexity, encodes each segment, and iteratively adjusts CRF
to meet the target VMAF quality.

Note: This is segment-level adaptation (not per-frame/per-macroblock like
Beamr CABR). It provides a coarse outer loop on top of the encoder's own
internal rate-distortion optimization.`,
	RunE: runPersegmentAnalyze,
}

func init() {
	persegmentAnalyzeCmd.Flags().StringVarP(&pfInput, "input", "i", "", "source video file")
	persegmentAnalyzeCmd.Flags().StringVar(&pfCodec, "codec", "libx264", "video codec")
	persegmentAnalyzeCmd.Flags().StringVar(&pfPreset, "preset", "medium", "encoding preset")
	persegmentAnalyzeCmd.Flags().Float64Var(&pfTargetVMAF, "target-vmaf", 93, "target VMAF quality")
	persegmentAnalyzeCmd.Flags().Float64Var(&pfTolerance, "tolerance", 2.0, "VMAF tolerance (+/-)")
	persegmentAnalyzeCmd.Flags().IntVar(&pfMinCRF, "min-crf", 15, "minimum CRF (max quality)")
	persegmentAnalyzeCmd.Flags().IntVar(&pfMaxCRF, "max-crf", 45, "maximum CRF (min quality)")
	persegmentAnalyzeCmd.Flags().IntVar(&pfMaxIter, "max-iter", 3, "max iterations per segment")

	mustMarkRequired(persegmentAnalyzeCmd, "input")

	persegmentCmd.AddCommand(persegmentAnalyzeCmd)
	rootCmd.AddCommand(persegmentCmd)
}

func runPersegmentAnalyze(cmd *cobra.Command, args []string) error {
	cfg := persegment.Config{
		TargetVMAF:      pfTargetVMAF,
		Tolerance:       pfTolerance,
		MinCRF:          pfMinCRF,
		MaxCRF:          pfMaxCRF,
		Codec:           ffmpeg.Codec(pfCodec),
		Preset:          pfPreset,
		SegmentDuration: 2 * time.Second,
		MaxIterations:   pfMaxIter,
	}

	fmt.Println("╔════════════════════════════════════════╗")
	fmt.Println("║    VEO Segment-Level CRF Adaptation      ║")
	fmt.Println("╚════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Source:      %s\n", pfInput)
	fmt.Printf("  Codec:       %s\n", pfCodec)
	fmt.Printf("  Preset:      %s\n", pfPreset)
	fmt.Printf("  Target VMAF: %.1f (+/- %.1f)\n", pfTargetVMAF, pfTolerance)
	fmt.Printf("  CRF range:   %d - %d\n", pfMinCRF, pfMaxCRF)
	fmt.Printf("  Max iter:    %d per segment\n", pfMaxIter)
	fmt.Println()
	fmt.Println("  Analyzing complexity and adapting CRF per segment...")
	fmt.Println()

	result, err := persegment.Adapt(context.Background(), pfInput, cfg)
	if err != nil {
		return fmt.Errorf("segment-level adaptation failed: %w", err)
	}

	// lComplexity profile summary
	fmt.Printf("  Complexity Profile:\n")
	fmt.Printf("    Avg spatial:  %.3f\n", result.ComplexityProfile.AvgSpatial)
	fmt.Printf("    Avg temporal: %.1f\n", result.ComplexityProfile.AvgTemporal)
	fmt.Printf("    Overall:      %.1f / 100\n", result.ComplexityProfile.OverallScore)
	fmt.Println()

	// lPer-segment results
	fmt.Printf("  Segments (%d):\n\n", len(result.Segments))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "    #\tTime\tComplexity\tCRF\tBitrate\tVMAF\tIter\n")
	_, _ = fmt.Fprintf(w, "    -\t----\t----------\t---\t-------\t----\t----\n")
	for i, seg := range result.Segments {
		_, _ = fmt.Fprintf(w, "    %d\t%.1f-%.1fs\t%.0f\t%d\t%.0f kbps\t%.1f\t%d\n",
			i+1, seg.Start.Seconds(), seg.End.Seconds(),
			seg.Complexity, seg.CRF, seg.Bitrate, seg.VMAF, seg.Iterations)
	}
	_ = w.Flush()

	fmt.Println()
	fmt.Printf("  Result:\n")
	fmt.Printf("    Avg bitrate:  %.0f kbps\n", result.AvgBitrate)
	fmt.Printf("    Avg VMAF:     %.1f (target: %.1f)\n", result.AvgVMAF, result.TargetVMAF)
	fmt.Printf("    Duration:     %s\n", result.Duration.Truncate(time.Millisecond))
	fmt.Println()

	return nil
}
