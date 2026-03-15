package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/contextaware"
)

var contextawareCmd = &cobra.Command{
	Use:   "context-aware",
	Short: "Context-aware encoding (device-specific ladders)",
}

var (
	caInput    string
	caPreset   string
	caParallel int
	caDevices  []string
)

var contextawareAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Generate device-specific bitrate ladders",
	Long: `Runs per-title analysis with device-specific parameters (resolution caps,
VMAF models, codec preferences) to produce optimized ladders for mobile,
desktop, TV, and 4K TV viewing contexts.`,
	RunE: runContextAwareAnalyze,
}

func init() {
	contextawareAnalyzeCmd.Flags().StringVarP(&caInput, "input", "i", "", "source video file")
	contextawareAnalyzeCmd.Flags().StringVar(&caPreset, "preset", "veryfast", "encoding preset")
	contextawareAnalyzeCmd.Flags().IntVar(&caParallel, "parallel", 2, "max parallel encodes")
	contextawareAnalyzeCmd.Flags().StringSliceVar(&caDevices, "devices", []string{"mobile", "desktop", "tv"}, "device profiles (mobile, desktop, tv, tv_4k)")

	mustMarkRequired(contextawareAnalyzeCmd, "input")

	contextawareCmd.AddCommand(contextawareAnalyzeCmd)
	rootCmd.AddCommand(contextawareCmd)
}

func runContextAwareAnalyze(cmd *cobra.Command, args []string) error {
	// lSelect profiles
	var profiles []contextaware.Profile
	for _, d := range caDevices {
		switch contextaware.DeviceClass(d) {
		case contextaware.DeviceMobile:
			profiles = append(profiles, contextaware.MobileProfile())
		case contextaware.DeviceDesktop:
			profiles = append(profiles, contextaware.DesktopProfile())
		case contextaware.DeviceTV:
			profiles = append(profiles, contextaware.TVProfile())
		case contextaware.DeviceTV4K:
			profiles = append(profiles, contextaware.TV4KProfile())
		default:
			return fmt.Errorf("unknown device: %s (use mobile, desktop, tv, tv_4k)", d)
		}
	}

	cfg := contextaware.Config{
		Profiles:  profiles,
		CRFValues: []int{18, 22, 26, 30, 34, 38, 42},
		Preset:    caPreset,
		Subsample: 5,
		Parallel:  caParallel,
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     VEO Context-Aware Analysis           ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Source:   %s\n", caInput)
	fmt.Printf("  Devices:  %v\n", caDevices)
	fmt.Printf("  Preset:   %s\n", caPreset)
	fmt.Println()

	progress := make(chan contextaware.Progress, 10)
	go func() {
		for p := range progress {
			fmt.Printf("\r  Analyzing for %s (%d/%d)...    ", p.DeviceName, p.DeviceDone, p.DeviceTotal)
		}
	}()

	result, err := contextaware.Analyze(context.Background(), caInput, cfg, progress)
	close(progress)
	if err != nil {
		return fmt.Errorf("context-aware analysis failed: %w", err)
	}

	fmt.Printf("\r  Analysis complete (%s)                              \n\n",
		result.Duration.Truncate(time.Millisecond))

	// lShow ladder for each device
	for _, dev := range result.Devices {
		fmt.Printf("  %s (%s):\n", dev.Profile.Name, dev.Profile.Description)
		fmt.Printf("    VMAF model: %s\n", dev.Profile.VMAFModel)
		fmt.Printf("    Max res:    %s\n", dev.Profile.MaxRes.Label())
		fmt.Printf("    Codecs:     %v\n", dev.Profile.Codecs)
		fmt.Printf("    Hull:       %d points\n", len(dev.Hull.Points))
		fmt.Printf("    Trials:     %d\n", dev.TrialCount)
		fmt.Println()

		if len(dev.Ladder.Rungs) > 0 {
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "      #\tRes\tCodec\tCRF\tBitrate\tVMAF\n")
			_, _ = fmt.Fprintf(w, "      -\t---\t-----\t---\t-------\t----\n")
			for _, r := range dev.Ladder.Rungs {
				_, _ = fmt.Fprintf(w, "      %d\t%s\t%s\t%d\t%.0f kbps\t%.1f\n",
					r.Index+1, r.Resolution.Label(), r.Codec, r.CRF, r.Bitrate, r.VMAF)
			}
			_ = w.Flush()

			minB, maxB := dev.Ladder.BitrateRange()
			minQ, maxQ := dev.Ladder.QualityRange()
			fmt.Printf("\n      Bitrate: %.0f - %.0f kbps | Quality: %.1f - %.1f VMAF\n", minB, maxB, minQ, maxQ)
		}
		fmt.Println()
	}

	return nil
}
