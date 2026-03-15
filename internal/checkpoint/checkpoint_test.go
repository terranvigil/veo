//go:build unit

package checkpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
)

func TestCheckpointSaveAndRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.checkpoint.json")
	hash := "abc123"

	// Create checkpoint and save a result
	cp, err := New(path, hash, "test.mp4")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	point := hull.Point{
		Resolution: ffmpeg.Res720p,
		Codec:      ffmpeg.CodecX264,
		CRF:        28,
		Bitrate:    1500,
		VMAF:       92.5,
	}

	if err := cp.Save("720p", "libx264", 28, point); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if !cp.IsCompleted("720p", "libx264", 28) {
		t.Error("expected trial to be completed after save")
	}
	if cp.IsCompleted("720p", "libx264", 30) {
		t.Error("unexpected completion for unsaved trial")
	}
	if cp.CompletedCount() != 1 {
		t.Errorf("expected 1 completed, got %d", cp.CompletedCount())
	}

	// Restore from file
	cp2, err := New(path, hash, "test.mp4")
	if err != nil {
		t.Fatalf("New (restore): %v", err)
	}

	if cp2.CompletedCount() != 1 {
		t.Fatalf("restored checkpoint should have 1 completed, got %d", cp2.CompletedCount())
	}

	restored, ok := cp2.Get("720p", "libx264", 28)
	if !ok {
		t.Fatal("expected to find saved trial in restored checkpoint")
	}
	if restored.VMAF != 92.5 {
		t.Errorf("restored VMAF = %f, want 92.5", restored.VMAF)
	}
	if restored.Bitrate != 1500 {
		t.Errorf("restored bitrate = %f, want 1500", restored.Bitrate)
	}
}

func TestCheckpointInvalidatedByConfigChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.checkpoint.json")

	// Save with hash "abc"
	cp1, _ := New(path, "abc", "test.mp4")
	_ = cp1.Save("720p", "libx264", 28, hull.Point{VMAF: 90})

	// Load with different hash "xyz" - should not restore
	cp2, _ := New(path, "xyz", "test.mp4")
	if cp2.CompletedCount() != 0 {
		t.Errorf("config change should invalidate checkpoint, got %d completed", cp2.CompletedCount())
	}
}

func TestCheckpointRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.checkpoint.json")

	cp, _ := New(path, "abc", "test.mp4")
	_ = cp.Save("720p", "libx264", 28, hull.Point{})

	if _, err := os.Stat(path); err != nil {
		t.Fatal("checkpoint file should exist after save")
	}

	_ = cp.Remove()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("checkpoint file should be removed")
	}
}

func TestConfigHash(t *testing.T) {
	h1 := ConfigHash("a.mp4", []string{"720p"}, []string{"libx264"}, []int{22, 28}, "fast")
	h2 := ConfigHash("a.mp4", []string{"720p"}, []string{"libx264"}, []int{22, 28}, "fast")
	h3 := ConfigHash("a.mp4", []string{"720p"}, []string{"libx264"}, []int{22, 30}, "fast")

	if h1 != h2 {
		t.Error("same config should produce same hash")
	}
	if h1 == h3 {
		t.Error("different CRF values should produce different hash")
	}
	if len(h1) != 16 {
		t.Errorf("hash length = %d, want 16", len(h1))
	}
}
