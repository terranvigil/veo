//go:build unit

package encoding

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanStaleTempDirs(t *testing.T) {
	// Create a fake temp dir structure
	tmpRoot := t.TempDir()

	// Create a "stale" veo dir (modified 2 days ago)
	staleDir := filepath.Join(tmpRoot, "veo-pertitle-stale123")
	if err := os.Mkdir(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Backdate the modification time
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(staleDir, old, old); err != nil {
		t.Fatal(err)
	}

	// Create a "fresh" veo dir (just created)
	freshDir := filepath.Join(tmpRoot, "veo-pershot-fresh456")
	if err := os.Mkdir(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a non-veo dir (should not be touched)
	otherDir := filepath.Join(tmpRoot, "other-dir")
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(otherDir, old, old); err != nil {
		t.Fatal(err)
	}

	// Run cleanup with custom root (we can't override os.TempDir, so test the logic directly)
	cleanTempDirsInRoot(tmpRoot, 24*time.Hour)

	// Stale veo dir should be removed
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Error("stale veo dir should have been removed")
	}

	// Fresh veo dir should remain
	if _, err := os.Stat(freshDir); err != nil {
		t.Error("fresh veo dir should not be removed")
	}

	// Non-veo dir should remain
	if _, err := os.Stat(otherDir); err != nil {
		t.Error("non-veo dir should not be touched")
	}
}
