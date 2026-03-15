package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/terranvigil/veo/internal/encoding"
)

var verbose bool

var rootCmd = &cobra.Command{
	Use:   "veo",
	Short: "Video Encoding Optimizer",
	Long:  "Content-aware video encoding optimization tool for per-title and per-shot encoding.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		level := slog.LevelWarn
		if verbose {
			level = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})))

		// clean up stale temp dirs from previous crashed runs
		encoding.CleanStaleTempDirs(24 * time.Hour)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
