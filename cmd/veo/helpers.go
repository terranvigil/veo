package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// panics on failure since flag registration is a programming error, not runtime.
func mustMarkRequired(cmd *cobra.Command, name string) {
	if err := cmd.MarkFlagRequired(name); err != nil {
		panic("failed to mark flag required: " + err.Error())
	}
}

// fails fast if the file does not exist or is a directory.
func validateInputFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("input file not found: %s", path)
	}
	if info.IsDir() {
		return fmt.Errorf("input is a directory, not a file: %s", path)
	}
	return nil
}

// fails fast if the parent directory does not exist.
func validateOutputDir(path string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("output directory does not exist: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("output path parent is not a directory: %s", dir)
	}
	return nil
}
