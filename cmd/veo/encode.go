package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

var encodeCmd = &cobra.Command{
	Use:   "encode <input>",
	Short: "Encode a video file",
	Args:  cobra.ExactArgs(1),
	RunE:  runEncode,
}

var (
	encodeOutput string
	encodeCodec  string
	encodeCRF    int
	encodePreset string
	encodeWidth  int
	encodeHeight int
)

func init() {
	encodeCmd.Flags().StringVarP(&encodeOutput, "output", "o", "", "output file path")
	encodeCmd.Flags().StringVar(&encodeCodec, "codec", "libx264", "video codec (libx264, libx265, libsvtav1)")
	encodeCmd.Flags().IntVar(&encodeCRF, "crf", 23, "CRF value")
	encodeCmd.Flags().StringVar(&encodePreset, "preset", "medium", "encoding preset")
	encodeCmd.Flags().IntVar(&encodeWidth, "width", 0, "output width (0 = keep original)")
	encodeCmd.Flags().IntVar(&encodeHeight, "height", 0, "output height (0 = keep original)")

	mustMarkRequired(encodeCmd, "output")

	rootCmd.AddCommand(encodeCmd)
}

func runEncode(cmd *cobra.Command, args []string) error {
	input := args[0]

	job := ffmpeg.EncodeJob{
		Input:  input,
		Output: encodeOutput,
		Codec:  ffmpeg.Codec(encodeCodec),
		CRF:    encodeCRF,
		Preset: encodePreset,
	}

	if encodeWidth > 0 && encodeHeight > 0 {
		job.Resolution = ffmpeg.Resolution{Width: encodeWidth, Height: encodeHeight}
	}

	// lProgress channel
	progress := make(chan ffmpeg.Progress, 10)
	go func() {
		for p := range progress {
			fmt.Printf("\rFrame: %d  FPS: %.1f  Bitrate: %.0f kbps  Speed: %.1fx  Time: %s",
				p.Frame, p.FPS, p.Bitrate, p.Speed, p.Time.Truncate(100*time.Millisecond))
		}
	}()

	result, err := ffmpeg.Encode(context.Background(), job, progress)
	close(progress)

	if err != nil {
		return fmt.Errorf("encode failed: %w", err)
	}

	fmt.Println() // newline after progress
	fmt.Printf("\nEncode complete:\n")
	fmt.Printf("  Output:    %s\n", filepath.Base(result.Job.Output))
	fmt.Printf("  Codec:     %s\n", result.Job.Codec)
	fmt.Printf("  CRF:       %d\n", result.Job.CRF)
	fmt.Printf("  Bitrate:   %.0f kbps\n", result.Bitrate)
	fmt.Printf("  File size: %s\n", formatBytes(result.FileSize))
	fmt.Printf("  Time:      %s\n", result.Duration.Truncate(100*time.Millisecond))

	return nil
}
