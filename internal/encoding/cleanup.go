package encoding

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CleanStaleTempDirs removes orphaned VEO temp directories older than maxAge.
// Called at startup to clean up after crashes or SIGKILL.
func CleanStaleTempDirs(maxAge time.Duration) {
	cleanTempDirsInRoot(os.TempDir(), maxAge)
}

// testable version that accepts a root directory.
func cleanTempDirsInRoot(root string, maxAge time.Duration) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), "veo-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > maxAge {
			path := filepath.Join(root, e.Name())
			slog.Debug("cleaning stale temp dir", "path", path, "age", time.Since(info.ModTime()))
			_ = os.RemoveAll(path)
		}
	}
}
