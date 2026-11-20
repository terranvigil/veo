package main

import (
	"errors"
	"fmt"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/compare"
)

const compareKeys = `
Keyboard shortcuts (press ? in the player for the full list):
  Space        Play or pause
  Left/Right   Step one frame (Shift: one second)
  N / P        Next or previous jump point
  S / D        Split view, side by side
  1 / 2 / 3    Show only A, only B, only the source
  F            Flip A and B
  Z            Zoom: fit, 2x, 4x, 8x`

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare two encodes of one source in the browser",
	Long: `Opens a browser player that puts two encodes of the same source next to
each other, locked to the same frame. Give it each encode's per-frame
VMAF log and it draws both curves on one timeline, shades where each
encode is ahead, and lists the places worth looking at: each encode's
worst dips and the moments where the two differ most.

Add --c and --d for more encodes: the viewer picks which two are on
screen. With only --a it compares one encode against --reference.
` + compareKeys,
	Example: `  veo compare --a ours.mp4 --b x265.mp4 \
    --vmaf-a ours.vmaf.json --vmaf-b x265.vmaf.json \
    --label-a yah265 --label-b x265 --reference source.mp4`,
	RunE: runCompare,
}

var compareExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write the player as a static folder for GitHub Pages",
	Long: `Writes a self-contained folder: index.html, its CSS and JS, a copy of
each video under media/ and manifest.json. Any static host can serve it,
GitHub Pages included. Nothing in it loads from another site.`,
	Example: `  veo compare export --out site/compare \
    --a ours.mp4 --b x265.mp4 \
    --vmaf-a ours.vmaf.json --vmaf-b x265.vmaf.json \
    --label-a yah265 --label-b x265 --notes how-made.txt`,
	RunE: runCompareExport,
}

var (
	cmpOpts   compare.Options
	cmpPort   int
	cmpNoOpen bool
	cmpOut    string

	cmpVariants   []string
	cmpIndexTitle string
)

var comparePreviewCmd = &cobra.Command{
	Use:   "preview DIR",
	Short: "Serve an exported folder locally",
	Long: `Serves an export, or a folder of exports, on localhost the way a static
host would, with the range requests video seeking needs.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return compare.Preview(ctx, args[0], cmpPort, !cmpNoOpen)
	},
}

var compareIndexCmd = &cobra.Command{
	Use:   "index DIR",
	Short: "Write an index page over the exports in a folder",
	Long: `Writes DIR/index.html listing every export found one level down
(DIR/*/manifest.json), which makes DIR ready to publish as one site, and
prints the size of the whole folder.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		n, total, err := compare.WriteIndex(args[0], cmpIndexTitle)
		if err != nil {
			return err
		}
		fmt.Printf("Wrote %s listing %d comparisons\nTotal size: %s\n", filepath.Join(args[0], "index.html"), n, compare.FormatBytes(total))
		return nil
	},
}

func init() {
	f := compareCmd.PersistentFlags()
	for i := range cmpOpts.Encodes {
		id := string(rune('a' + i))
		in := &cmpOpts.Encodes[i]
		what := "encode " + strings.ToUpper(id)
		extra := ""
		if i >= 2 {
			extra = " (a further encode the viewer can put on either side)"
		}
		f.StringVar(&in.Path, id, "", what+": a browser-playable video, e.g. MP4"+extra)
		f.StringVar(&in.Scores, "vmaf-"+id, "", "per-frame VMAF JSON for "+what+" (libvmaf log, or veo quality measure --per-frame -o)")
		f.StringVar(&in.Label, "label-"+id, "", "name shown for "+what+", e.g. the encoder")
		f.StringVar(&in.Detail, "detail-"+id, "", "settings shown beside "+what+", e.g. \"medium, CRF 26\"")
		f.StringVar(&in.Fallback, "fallback-"+id, "", "file to play for "+what+" where the browser can't decode it, e.g. an H.264 proxy")
	}
	f.StringVar(&cmpOpts.Ref.Path, "reference", "", "source or near-lossless reference video (optional)")
	f.StringVar(&cmpOpts.Ref.Label, "label-reference", "", `name shown for the reference (default "Source")`)
	f.StringVar(&cmpOpts.Ref.Fallback, "fallback-reference", "", "file to play for the reference where the browser can't decode it")
	f.StringVar(&cmpOpts.RefURL, "reference-url", "", "URL of a reference hosted elsewhere, instead of --reference; nothing is copied")
	f.BoolVar(&cmpOpts.NoSegments, "no-segments", false, "use the whole MP4s as they are, without cutting fragmented MP4 segments")
	f.StringArrayVar(&cmpVariants, "variant", nil, `another resolution of this title, as LABEL=HREF; LABEL alone names this one (e.g. --variant 1080p --variant 4K=../tears-4k/)`)
	f.StringVar(&cmpOpts.Title, "title", "", "title of the comparison (default: A's file name)")
	f.StringVar(&cmpOpts.Subtitle, "subtitle", "", "one line under the title")
	f.StringVar(&cmpOpts.NotesFile, "notes", "", `"how this was made" text: a .json notes file, or plain text`)
	f.Float64Var(&cmpOpts.FPS, "fps", 0, "frame rate (default: probed from A)")
	f.StringVar(&cmpOpts.MetricKey, "metric", "vmaf", "metric name under frames[].metrics in the logs")
	f.StringVar(&cmpOpts.Metric, "metric-label", "", "metric name the player prints (default from --metric)")
	f.StringVar(&cmpOpts.Model, "model", "", "quality model named in the player, e.g. vmaf_v0.6.1")
	f.IntVar(&cmpOpts.PerKind, "moments", 4, "jump points listed per kind")

	// the single-encode flags from the first player
	f.StringVar(&cmpOpts.Encodes[0].Path, "encoded", "", "encode A")
	f.StringVar(&cmpOpts.Encodes[0].Scores, "vmaf-data", "", "per-frame VMAF JSON for A")
	_ = f.MarkDeprecated("encoded", "use --a")
	_ = f.MarkDeprecated("vmaf-data", "use --vmaf-a")

	compareCmd.Flags().IntVar(&cmpPort, "port", 8787, "HTTP port for the player")
	compareCmd.Flags().BoolVar(&cmpNoOpen, "no-open", false, "don't open the browser")

	compareExportCmd.Flags().StringVarP(&cmpOut, "out", "o", "", "folder to write")
	compareExportCmd.Flags().StringVar(&cmpOpts.MediaBase, "media-base", "", `where the page loads videos from (default "media/", beside the page); a URL moves them to other hosting`)
	compareExportCmd.Flags().BoolVar(&cmpOpts.NoProgressive, "no-progressive", false, "host only the segments, without the whole MP4s")

	compareIndexCmd.Flags().StringVar(&cmpIndexTitle, "title", "Encoder comparisons", "heading of the index page")
	compareCmd.AddCommand(compareIndexCmd)

	comparePreviewCmd.Flags().IntVar(&cmpPort, "port", 8787, "HTTP port")
	comparePreviewCmd.Flags().BoolVar(&cmpNoOpen, "no-open", false, "don't open the browser")
	compareCmd.AddCommand(comparePreviewCmd)
	mustMarkRequired(compareExportCmd, "out")

	compareCmd.AddCommand(compareExportCmd)
	rootCmd.AddCommand(compareCmd)
}

func validateCompare() error {
	if cmpOpts.Encodes[0].Path == "" {
		return errors.New("--a is required")
	}
	if cmpOpts.Ref.Path != "" && cmpOpts.RefURL != "" {
		return errors.New("give --reference or --reference-url, not both")
	}
	cmpOpts.Variants = nil
	for _, v := range cmpVariants {
		label, href, _ := strings.Cut(v, "=")
		if label == "" {
			return fmt.Errorf("--variant %q needs a label", v)
		}
		cmpOpts.Variants = append(cmpOpts.Variants, compare.Variant{Label: label, Href: href})
	}
	type input struct{ flag, path string }
	inputs := []input{
		{"reference", cmpOpts.Ref.Path}, {"fallback-reference", cmpOpts.Ref.Fallback}, {"notes", cmpOpts.NotesFile},
	}
	for i, in := range cmpOpts.Encodes {
		id := string(rune('a' + i))
		if in.Path == "" && (in.Scores != "" || in.Fallback != "") {
			return fmt.Errorf("--vmaf-%s and --fallback-%s need --%s", id, id, id)
		}
		inputs = append(inputs, input{id, in.Path}, input{"vmaf-" + id, in.Scores}, input{"fallback-" + id, in.Fallback})
	}
	for _, in := range inputs {
		if in.path == "" {
			continue
		}
		if err := validateInputFile(in.path); err != nil {
			return fmt.Errorf("--%s: %w", in.flag, err)
		}
	}
	return nil
}

func runCompare(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unknown argument %q", args[0])
	}
	if err := validateCompare(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return compare.Serve(ctx, &cmpOpts, cmpPort, !cmpNoOpen)
}

func runCompareExport(cmd *cobra.Command, _ []string) error {
	if err := validateCompare(); err != nil {
		return err
	}
	res, err := compare.Export(cmd.Context(), &cmpOpts, cmpOut)
	if err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", cmpOut)
	compare.PrintSummary(res.Manifest)
	fmt.Printf("  Total size: %s\n", compare.FormatBytes(res.Bytes))
	fmt.Printf("\nServe it with any static host, or locally:\n  veo compare preview %s\n", cmpOut)
	return nil
}
