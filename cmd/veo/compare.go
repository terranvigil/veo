package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/compare"
)

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Launch side-by-side video comparison player",
	Long: `Opens a browser-based comparison player showing the reference and encoded
video side by side with a draggable slider. If per-frame VMAF data is
provided, displays a quality timeline with dip markers for scrubbing to
problem areas.

Keyboard shortcuts:
  Space       Play/pause
  Left/Right  Step frame by frame
  [ / ]       Jump to previous/next quality dip`,
	RunE: runCompare,
}

var (
	cmpReference string
	cmpEncoded   string
	cmpVMAFData  string
	cmpPort      int
)

func init() {
	compareCmd.Flags().StringVar(&cmpReference, "reference", "", "reference (original) video file")
	compareCmd.Flags().StringVar(&cmpEncoded, "encoded", "", "encoded video file")
	compareCmd.Flags().StringVar(&cmpVMAFData, "vmaf-data", "", "per-frame VMAF JSON file (from veo quality measure --per-frame)")
	compareCmd.Flags().IntVar(&cmpPort, "port", 8787, "HTTP port for the player")

	mustMarkRequired(compareCmd, "reference")
	mustMarkRequired(compareCmd, "encoded")

	rootCmd.AddCommand(compareCmd)
}

func runCompare(cmd *cobra.Command, args []string) error {
	if err := validateInputFile(cmpReference); err != nil {
		return fmt.Errorf("reference: %w", err)
	}
	if err := validateInputFile(cmpEncoded); err != nil {
		return fmt.Errorf("encoded: %w", err)
	}

	return compare.Serve(compare.Opts{
		Reference: cmpReference,
		Encoded:   cmpEncoded,
		VMAFData:  cmpVMAFData,
		Port:      cmpPort,
	})
}
