package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect video files",
}

var inspectProbeCmd = &cobra.Command{
	Use:   "probe <file>",
	Short: "Show video file metadata via ffprobe",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspectProbe,
}

func init() {
	inspectCmd.AddCommand(inspectProbeCmd)
	rootCmd.AddCommand(inspectCmd)
}

func runInspectProbe(cmd *cobra.Command, args []string) error {
	path := args[0]

	result, err := ffmpeg.Probe(context.Background(), path)
	if err != nil {
		return fmt.Errorf("probe failed: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

	_, _ = fmt.Fprintf(w, "File:\t%s\n", result.Format.Filename)
	_, _ = fmt.Fprintf(w, "Format:\t%s\n", result.Format.FormatLongName)
	_, _ = fmt.Fprintf(w, "Duration:\t%s\n", result.Format.DurationTime())
	_, _ = fmt.Fprintf(w, "Size:\t%s\n", formatBytes(result.Format.Size))
	if result.Format.BitRate > 0 {
		_, _ = fmt.Fprintf(w, "Bitrate:\t%s\n", formatBitrate(result.Format.BitRate))
	}
	_, _ = fmt.Fprintln(w)

	for _, s := range result.Streams {
		_, _ = fmt.Fprintf(w, "Stream #%d:\t%s\n", s.Index, s.CodecType)
		_, _ = fmt.Fprintf(w, "  Codec:\t%s", s.CodecName)
		if s.Profile != "" {
			_, _ = fmt.Fprintf(w, " (%s)", s.Profile)
		}
		_, _ = fmt.Fprintln(w)

		if s.CodecType == "video" {
			_, _ = fmt.Fprintf(w, "  Resolution:\t%s\n", s.Resolution())
			_, _ = fmt.Fprintf(w, "  Pixel Format:\t%s\n", s.PixFmt)
			if fps := s.FPS(); fps > 0 {
				_, _ = fmt.Fprintf(w, "  Frame Rate:\t%.2f fps\n", fps)
			}
			if s.NbFrames > 0 {
				_, _ = fmt.Fprintf(w, "  Frames:\t%d\n", s.NbFrames)
			}
			if s.BitsPerSample > 0 {
				_, _ = fmt.Fprintf(w, "  Bit Depth:\t%d-bit\n", s.BitsPerSample)
			}
			if s.ColorSpace != "" {
				_, _ = fmt.Fprintf(w, "  Color Space:\t%s\n", s.ColorSpace)
			}
			if s.ColorTransfer != "" {
				_, _ = fmt.Fprintf(w, "  Transfer:\t%s\n", s.ColorTransfer)
			}
			if s.ColorPrimaries != "" {
				_, _ = fmt.Fprintf(w, "  Primaries:\t%s\n", s.ColorPrimaries)
			}
		}

		if s.CodecType == "audio" {
			if s.SampleRate > 0 {
				_, _ = fmt.Fprintf(w, "  Sample Rate:\t%d Hz\n", s.SampleRate)
			}
			if s.Channels > 0 {
				_, _ = fmt.Fprintf(w, "  Channels:\t%d", s.Channels)
				if s.ChannelLayout != "" {
					_, _ = fmt.Fprintf(w, " (%s)", s.ChannelLayout)
				}
				_, _ = fmt.Fprintln(w)
			}
		}

		if s.BitRate > 0 {
			_, _ = fmt.Fprintf(w, "  Bitrate:\t%s\n", formatBitrate(s.BitRate))
		}
		_, _ = fmt.Fprintln(w)
	}

	return w.Flush()
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatBitrate(bps int64) string {
	switch {
	case bps >= 1_000_000:
		return fmt.Sprintf("%.2f Mbps", float64(bps)/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.0f kbps", float64(bps)/1_000)
	default:
		return fmt.Sprintf("%d bps", bps)
	}
}
