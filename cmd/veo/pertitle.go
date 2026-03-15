package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/chart"
	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
	"github.com/terranvigil/veo/internal/pertitle"
)

var pertitleCmd = &cobra.Command{
	Use:   "per-title",
	Short: "Per-title encoding optimization",
}

var (
	ptInput       string
	ptOutput      string
	ptChartDir    string
	ptCodecs      []string
	ptResolutions []string
	ptCRFValues   []int
	ptPreset      string
	ptSubsample   int
	ptParallel    int
	ptNumRungs    int
	ptMinBitrate  float64
	ptMaxBitrate  float64
	ptDryRun      bool
	ptCheckpoint  string
	ptMode        string
	ptEncodeDir   string
)

var pertitleAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Run per-title analysis to compute optimal bitrate ladder",
	Long: `Encodes the source video at multiple (resolution, CRF, codec) combinations,
measures VMAF quality for each, computes the convex hull (Pareto frontier),
and selects an optimal bitrate ladder.`,
	RunE: runPertitleAnalyze,
}

func init() {
	pertitleAnalyzeCmd.Flags().StringVarP(&ptInput, "input", "i", "", "source video file")
	pertitleAnalyzeCmd.Flags().StringVarP(&ptOutput, "output", "o", "", "output JSON file for results (optional)")
	pertitleAnalyzeCmd.Flags().StringVar(&ptChartDir, "charts", "", "directory to save PNG chart images (optional)")
	pertitleAnalyzeCmd.Flags().StringSliceVar(&ptCodecs, "codecs", []string{"libx264"}, "codecs to test (libx264, libx265, libsvtav1)")
	pertitleAnalyzeCmd.Flags().StringSliceVar(&ptResolutions, "resolutions", []string{"480p", "720p", "1080p"}, "resolutions to test")
	pertitleAnalyzeCmd.Flags().IntSliceVar(&ptCRFValues, "crf-values", []int{18, 22, 26, 30, 34, 38, 42}, "CRF values to test")
	pertitleAnalyzeCmd.Flags().StringVar(&ptPreset, "preset", "veryfast", "encoding preset for trial encodes")
	pertitleAnalyzeCmd.Flags().IntVar(&ptSubsample, "subsample", 5, "VMAF frame subsampling (0 = every frame)")
	pertitleAnalyzeCmd.Flags().IntVar(&ptParallel, "parallel", 2, "max parallel encodes")
	pertitleAnalyzeCmd.Flags().IntVar(&ptNumRungs, "rungs", 6, "number of ladder rungs")
	pertitleAnalyzeCmd.Flags().Float64Var(&ptMinBitrate, "min-bitrate", 200, "minimum bitrate (kbps)")
	pertitleAnalyzeCmd.Flags().Float64Var(&ptMaxBitrate, "max-bitrate", 8000, "maximum bitrate (kbps)")
	pertitleAnalyzeCmd.Flags().BoolVar(&ptDryRun, "dry-run", false, "show what would be encoded without running")
	pertitleAnalyzeCmd.Flags().StringVar(&ptCheckpoint, "checkpoint", "", "checkpoint file for resume support (default: none)")
	pertitleAnalyzeCmd.Flags().StringVar(&ptMode, "mode", "crf", "rate control mode for trial encodes: crf (default) or qp (fixed quantizer, Netflix-style)")
	pertitleAnalyzeCmd.Flags().StringVar(&ptEncodeDir, "encode-output", "", "directory to write final encoded files at each ladder rung (optional)")

	mustMarkRequired(pertitleAnalyzeCmd, "input")

	pertitleCmd.AddCommand(pertitleAnalyzeCmd)
	rootCmd.AddCommand(pertitleCmd)
}

func runPertitleAnalyze(cmd *cobra.Command, args []string) error {
	// validate inputs early
	if err := validateInputFile(ptInput); err != nil {
		return err
	}
	if err := validateOutputDir(ptOutput); err != nil {
		return err
	}

	// lParse resolutions
	resolutions, err := parseResolutions(ptResolutions)
	if err != nil {
		return err
	}

	// lParse codecs
	codecs := make([]ffmpeg.Codec, len(ptCodecs))
	for i, c := range ptCodecs {
		codecs[i] = ffmpeg.Codec(c)
	}

	cfg := pertitle.Config{
		Config: encoding.Config{
			Resolutions: resolutions,
			CRFValues:   ptCRFValues,
			Codecs:      codecs,
			Preset:      ptPreset,
			Subsample:   ptSubsample,
			Parallel:    ptParallel,
			RateControl: ffmpeg.RateControlMode(ptMode),
		},
		LadderOpts:     ladderOpts(ptNumRungs, ptMinBitrate, ptMaxBitrate),
		CheckpointPath: ptCheckpoint,
	}

	totalTrials := len(resolutions) * len(codecs) * len(ptCRFValues)
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║        VEO Per-Title Analysis            ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Source:      %s\n", ptInput)
	fmt.Printf("  Resolutions: %v\n", ptResolutions)
	fmt.Printf("  Codecs:      %v\n", ptCodecs)
	fmt.Printf("  CRF values:  %v\n", ptCRFValues)
	fmt.Printf("  Preset:      %s\n", ptPreset)
	fmt.Printf("  Trials:      %d (%d res x %d CRF x %d codecs)\n",
		totalTrials, len(resolutions), len(ptCRFValues), len(codecs))
	fmt.Printf("  Parallel:    %d\n", cfg.EffectiveParallel())
	fmt.Println()

	if ptDryRun {
		fmt.Println("  [DRY RUN] Would encode the following trials:")
		fmt.Println()
		for _, res := range resolutions {
			for _, codec := range codecs {
				for _, crf := range ptCRFValues {
					fmt.Printf("    %s %s CRF %d (preset: %s)\n",
						res.Label(), codec, crf, encoding.PresetForCodec(codec, ptPreset))
				}
			}
		}
		fmt.Println()
		fmt.Printf("  Total: %d trial encodes\n", totalTrials)
		return nil
	}

	// lProgress channel - overwrite line for clean output
	progress := make(chan pertitle.TrialProgress, 10)
	go func() {
		for p := range progress {
			fmt.Printf("\r  Encoding: [%d/%d] %s %s CRF %d → %.0f kbps, VMAF %.1f    ",
				p.Done, p.Total,
				p.Resolution.Label(), p.Codec, p.CRF,
				p.Bitrate, p.VMAF)
		}
	}()

	result, err := pertitle.Analyze(context.Background(), ptInput, cfg, progress)
	close(progress)
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	// lClear the progress line
	fmt.Printf("\r  Encoding: [%d/%d] complete                                          \n",
		result.TrialCount, result.TrialCount)
	avgPerTrial := result.Duration / time.Duration(result.TrialCount)
	fmt.Printf("  Duration: %s (%.1fs avg per trial)\n",
		result.Duration.Truncate(time.Millisecond), avgPerTrial.Seconds())
	fmt.Println()

	// ── Convex Hull ──
	fmt.Printf("  Convex Hull (%d Pareto-optimal points):\n\n", len(result.Hull.Points))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "    Res\tCodec\tCRF\tBitrate\tVMAF\n")
	_, _ = fmt.Fprintf(w, "    ---\t-----\t---\t-------\t----\n")
	for _, p := range result.Hull.Points {
		_, _ = fmt.Fprintf(w, "    %s\t%s\t%d\t%.0f kbps\t%.1f\n",
			p.Resolution.Label(), p.Codec, p.CRF, p.Bitrate, p.VMAF)
	}
	_ = w.Flush()
	fmt.Println()

	// ── Resolution Crossovers ──
	if len(result.Crossovers) > 0 {
		fmt.Println("  Resolution Crossovers:")
		for _, c := range result.Crossovers {
			fmt.Printf("    %s -> %s at ~%.0f kbps (VMAF ~%.1f)\n",
				c.From.Label(), c.To.Label(), c.Bitrate, c.VMAF)
		}
		fmt.Println()
	}

	// ── Optimized Ladder ──
	if len(result.Ladder.Rungs) > 0 {
		fmt.Printf("  Optimized Bitrate Ladder (%d rungs):\n\n", len(result.Ladder.Rungs))
		w = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "    #\tRes\tCodec\tCRF\tBitrate\tVMAF\n")
		_, _ = fmt.Fprintf(w, "    -\t---\t-----\t---\t-------\t----\n")
		for _, r := range result.Ladder.Rungs {
			_, _ = fmt.Fprintf(w, "    %d\t%s\t%s\t%d\t%.0f kbps\t%.1f\n",
				r.Index+1, r.Resolution.Label(), r.Codec, r.CRF, r.Bitrate, r.VMAF)
		}
		_ = w.Flush()

		minB, maxB := result.Ladder.BitrateRange()
		minQ, maxQ := result.Ladder.QualityRange()
		fmt.Printf("\n    Bitrate: %.0f - %.0f kbps | Quality: %.1f - %.1f VMAF\n", minB, maxB, minQ, maxQ)
		fmt.Println()
	}

	// ── Codec Comparison ──
	if len(result.PerCodec) > 1 {
		fmt.Println("  Codec Comparison:")
		codecList := make([]ffmpeg.Codec, 0, len(result.PerCodec))
		for c := range result.PerCodec {
			codecList = append(codecList, c)
		}
		for _, c := range codecList {
			h := result.PerCodec[c]
			fmt.Printf("    %s: %d hull points (%.0f - %.0f kbps)\n",
				c, len(h.Points), h.Points[0].Bitrate, h.Points[len(h.Points)-1].Bitrate)
		}
		if len(codecList) >= 2 {
			anchor := result.PerCodec[codecList[0]]
			for _, c := range codecList[1:] {
				other := result.PerCodec[c]
				bdrate, bdErr := hull.BDRate(anchor.Points, other.Points)
				if bdErr == nil {
					fmt.Printf("    BD-Rate (%s vs %s): %.1f%%\n", c, codecList[0], bdrate)
				}
			}
		}
		fmt.Println()
	}

	// ── Netflix Comparison ──
	netflixFixed := ladder.NetflixOld()
	if len(result.Ladder.Rungs) > 0 {
		topRung := result.Ladder.Rungs[len(result.Ladder.Rungs)-1]
		fmt.Printf("  vs %s:\n", netflixFixed.Name)
		fmt.Printf("    Fixed:     1080p at %.0f kbps (top rung)\n", netflixFixed.TopBitrate())
		fmt.Printf("    Optimized: %s at %.0f kbps, VMAF %.1f\n",
			topRung.Resolution.Label(), topRung.Bitrate, topRung.VMAF)
		if topRung.VMAF >= 93 {
			var fixedAtRes float64
			for _, r := range netflixFixed.Rungs {
				if r.Resolution.Height >= topRung.Resolution.Height {
					fixedAtRes = r.Bitrate
					break
				}
			}
			if fixedAtRes > 0 && topRung.Bitrate < fixedAtRes {
				savings := (1 - topRung.Bitrate/fixedAtRes) * 100
				fmt.Printf("    Savings:   %.0f%% at equivalent resolution\n", savings)
			}
		}
		fmt.Println()
	}

	// lGenerate charts if requested
	if ptChartDir != "" {
		if err := os.MkdirAll(ptChartDir, 0o755); err != nil {
			return fmt.Errorf("failed to create chart directory: %w", err)
		}

		// lR-D curve with hull
		rdData, err := chart.RDCurve(result.Points, result.Hull, chart.Opts{
			Title: fmt.Sprintf("R-D Curve: %s", filepath.Base(ptInput)),
		})
		if err != nil {
			return fmt.Errorf("failed to generate R-D chart: %w", err)
		}
		rdPath := ptChartDir + "/rd-curve.png"
		if err := chart.SavePNG(rdData, rdPath); err != nil {
			return fmt.Errorf("failed to save R-D chart: %w", err)
		}
		fmt.Printf("\nChart: %s\n", rdPath)

		// lPer-codec comparison (if multiple codecs)
		if len(result.PerCodec) > 1 {
			// lCompute BD-Rate for chart annotation
			var bdRateVal float64
			codecList := make([]ffmpeg.Codec, 0, len(result.PerCodec))
			for c := range result.PerCodec {
				codecList = append(codecList, c)
			}
			if len(codecList) >= 2 {
				bdRateVal, _ = hull.BDRate(result.PerCodec[codecList[0]].Points, result.PerCodec[codecList[1]].Points)
			}

			codecData, err := chart.PerCodecRDCurve(result.PerCodec, bdRateVal, chart.Opts{
				Title: fmt.Sprintf("Codec Comparison: %s", filepath.Base(ptInput)),
			})
			if err != nil {
				return fmt.Errorf("failed to generate codec chart: %w", err)
			}
			codecPath := ptChartDir + "/codec-comparison.png"
			if err := chart.SavePNG(codecData, codecPath); err != nil {
				return fmt.Errorf("failed to save codec chart: %w", err)
			}
			fmt.Printf("Chart: %s\n", codecPath)
		}

		// lLadder bar chart
		if len(result.Ladder.Rungs) > 0 {
			ladderData, err := chart.LadderChart(result.Ladder, chart.Opts{
				Title: fmt.Sprintf("Bitrate Ladder: %s", filepath.Base(ptInput)),
			})
			if err != nil {
				return fmt.Errorf("failed to generate ladder chart: %w", err)
			}
			ladderPath := ptChartDir + "/ladder.png"
			if err := chart.SavePNG(ladderData, ladderPath); err != nil {
				return fmt.Errorf("failed to save ladder chart: %w", err)
			}
			fmt.Printf("Chart: %s\n", ladderPath)
		}
	}

	// lSave JSON if requested
	if ptOutput != "" {
		if err := result.SaveJSON(ptOutput); err != nil {
			return fmt.Errorf("failed to save results: %w", err)
		}
		fmt.Printf("\nResults saved to: %s\n", ptOutput)
	}

	// lProduce final encoded files at each ladder rung
	if ptEncodeDir != "" && len(result.Ladder.Rungs) > 0 {
		if err := os.MkdirAll(ptEncodeDir, 0o755); err != nil {
			return fmt.Errorf("failed to create encode output directory: %w", err)
		}

		fmt.Printf("\n  Producing final encodes at optimized ladder rungs:\n\n")
		for i, rung := range result.Ladder.Rungs {
			outPath := filepath.Join(ptEncodeDir,
				fmt.Sprintf("rung%d_%s_%s_%dkbps.mp4",
					i+1, rung.Resolution.Label(), rung.Codec, int(rung.Bitrate)))

			job := ffmpeg.EncodeJob{
				Input:         ptInput,
				Output:        outPath,
				Resolution:    rung.Resolution,
				Codec:         rung.Codec,
				RateControl:   ffmpeg.RateControlVBR,
				TargetBitrate: rung.Bitrate,
				Preset:        "medium", // slower preset for final quality
			}

			fmt.Printf("    [%d/%d] %s %s @ %.0f kbps → %s\n",
				i+1, len(result.Ladder.Rungs),
				rung.Resolution.Label(), rung.Codec, rung.Bitrate,
				filepath.Base(outPath))

			if _, err := ffmpeg.Encode(context.Background(), job, nil); err != nil {
				fmt.Printf("    WARNING: encode failed: %v\n", err)
				continue
			}
		}
		fmt.Printf("\n  Final encodes saved to: %s/\n", ptEncodeDir)
	}

	return nil
}

func parseResolutions(names []string) ([]ffmpeg.Resolution, error) {
	var result []ffmpeg.Resolution
	for _, name := range names {
		name = strings.TrimSuffix(strings.ToLower(name), "p")
		switch name {
		case "240":
			result = append(result, ffmpeg.Res240p)
		case "360":
			result = append(result, ffmpeg.Res360p)
		case "480":
			result = append(result, ffmpeg.Res480p)
		case "720":
			result = append(result, ffmpeg.Res720p)
		case "1080":
			result = append(result, ffmpeg.Res1080p)
		case "1440":
			result = append(result, ffmpeg.Res1440p)
		case "2160":
			result = append(result, ffmpeg.Res2160p)
		default:
			return nil, fmt.Errorf("unknown resolution: %s (use 240p, 360p, 480p, 720p, 1080p, 1440p, 2160p)", name)
		}
	}
	return result, nil
}

func ladderOpts(numRungs int, minBitrate, maxBitrate float64) ladder.Opts {
	return ladder.Opts{
		NumRungs:   numRungs,
		MinBitrate: minBitrate,
		MaxBitrate: maxBitrate,
		MinVMAF:    40,
		MaxVMAF:    97,
	}
}
