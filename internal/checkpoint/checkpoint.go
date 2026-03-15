// Package checkpoint provides incremental save/restore of encoding trial
// results. This allows long-running analyses to resume after interruption
// without re-encoding already-completed trials.
//
// Checkpoint files are JSON, stored alongside the output or in a temp dir.
// A checkpoint is keyed by a hash of the input file path + encoding config,
// so changing any parameter invalidates the checkpoint.
package checkpoint

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/terranvigil/veo/internal/hull"
)

type State struct {
	ConfigHash string                `json:"config_hash"`
	Source     string                `json:"source"`
	Completed  map[string]hull.Point `json:"completed"` // key: "res_codec_crf"
}

// Checkpoint manages incremental trial result persistence.
type Checkpoint struct {
	mu    sync.Mutex
	path  string
	state State
	dirty bool
}

// New creates a checkpoint manager. If a checkpoint file exists at the given
// path with a matching config hash, completed trials are loaded. Otherwise
// a fresh checkpoint is started.
func New(path string, configHash string, source string) (*Checkpoint, error) {
	cp := &Checkpoint{
		path: path,
		state: State{
			ConfigHash: configHash,
			Source:     source,
			Completed:  make(map[string]hull.Point),
		},
	}

	// try to load existing checkpoint
	if data, err := os.ReadFile(path); err == nil {
		var existing State
		if err := json.Unmarshal(data, &existing); err == nil {
			if existing.ConfigHash == configHash && existing.Source == source {
				cp.state = existing
				if cp.state.Completed == nil {
					cp.state.Completed = make(map[string]hull.Point)
				}
			}
			// lDifferent config/source - start fresh (don't load stale data)
		}
	}

	return cp, nil
}

// IsCompleted checks if a trial has already been completed.
func (cp *Checkpoint) IsCompleted(resolution, codec string, crf int) bool {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	_, ok := cp.state.Completed[makeKey(resolution, codec, crf)]
	return ok
}

// Get returns a completed trial result, or false if not found.
func (cp *Checkpoint) Get(resolution, codec string, crf int) (hull.Point, bool) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	p, ok := cp.state.Completed[makeKey(resolution, codec, crf)]
	return p, ok
}

// Save records a completed trial and writes to disk atomically.
// Holds the lock through the entire operation to prevent inconsistent state.
func (cp *Checkpoint) Save(resolution, codec string, crf int, point hull.Point) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	cp.state.Completed[makeKey(resolution, codec, crf)] = point
	cp.dirty = true

	return cp.flushLocked()
}

// CompletedCount returns how many trials have been completed.
func (cp *Checkpoint) CompletedCount() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return len(cp.state.Completed)
}

// AllCompleted returns all completed trial results.
func (cp *Checkpoint) AllCompleted() []hull.Point {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	points := make([]hull.Point, 0, len(cp.state.Completed))
	for _, p := range cp.state.Completed {
		points = append(points, p)
	}
	return points
}

// Remove deletes the checkpoint file.
func (cp *Checkpoint) Remove() error {
	return os.Remove(cp.path)
}

// caller must hold cp.mu.
func (cp *Checkpoint) flushLocked() error {
	if !cp.dirty {
		return nil
	}

	data, err := json.MarshalIndent(cp.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	// write atomically via temp file + rename
	dir := filepath.Dir(cp.path)
	tmp, err := os.CreateTemp(dir, ".veo-checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp checkpoint: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("failed to write checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("failed to close checkpoint: %w", err)
	}
	if err := os.Rename(tmp.Name(), cp.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("failed to rename checkpoint: %w", err)
	}

	cp.dirty = false
	return nil
}

// ConfigHash computes a deterministic hash of encoding configuration
// parameters. Any change in config invalidates existing checkpoints.
func ConfigHash(source string, resolutions, codecs []string, crfValues []int, preset string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "source=%s\n", source)
	for _, r := range resolutions {
		_, _ = fmt.Fprintf(h, "res=%s\n", r)
	}
	for _, c := range codecs {
		_, _ = fmt.Fprintf(h, "codec=%s\n", c)
	}
	for _, crf := range crfValues {
		_, _ = fmt.Fprintf(h, "crf=%d\n", crf)
	}
	_, _ = fmt.Fprintf(h, "preset=%s\n", preset)
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// DefaultPath returns the default checkpoint file path for a given source.
func DefaultPath(source string) string {
	dir := filepath.Dir(source)
	base := filepath.Base(source)
	return filepath.Join(dir, ".veo-checkpoint-"+base+".json")
}

func makeKey(resolution, codec string, crf int) string {
	return fmt.Sprintf("%s_%s_%d", resolution, codec, crf)
}
