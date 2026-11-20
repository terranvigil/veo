//go:build integration

package compare

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

// encode makes a two-second clip with B-frames and several key frames.
func encode(t *testing.T, codec, name string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	args := []string{
		"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=4",
		"-c:v", codec, "-g", "24", "-bf", "3", "-pix_fmt", "yuv420p", "-movflags", "+faststart",
	}
	if codec == "libx265" {
		args = append(args, "-tag:v", "hvc1", "-x265-params", "log-level=error")
	}
	if msg, err := exec.CommandContext(context.Background(), ffmpeg.FFmpegPath(), append(args, out)...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't encode with %s here: %v\n%s", codec, err, msg)
	}
	return out
}

// The export repackages; it must never touch the encoder's output. This
// compares the elementary stream of each source file with the one read back
// from its segments.
func TestExportKeepsBitstream(t *testing.T) {
	ctx := context.Background()
	opts := &Options{}
	opts.Encodes[0] = Input{Path: encode(t, "libx265", "a.mp4"), Label: "hevc"}
	opts.Encodes[1] = Input{Path: encode(t, "libx264", "b.mp4"), Label: "avc"}
	out := t.TempDir()
	res, err := Export(ctx, opts, out)
	if err != nil {
		t.Fatal(err)
	}

	for i, enc := range res.Manifest.Encodes {
		seg := enc.Segments
		if seg == nil || len(seg.List) < 2 {
			t.Fatalf("%s: want several segments, got %+v", enc.Label, seg)
		}
		want, err := StreamHash(ctx, opts.Encodes[i].Path)
		if err != nil {
			t.Fatal(err)
		}

		// through the playlist, as Safari reads it
		got, err := StreamHash(ctx, filepath.Join(out, "media", filepath.FromSlash(seg.Playlist)))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: playlist stream %s, source %s", enc.Label, got, want)
		}

		// and as the bytes the player appends: init, then each segment in order
		joined, err := os.ReadFile(filepath.Join(out, "media", filepath.FromSlash(seg.Init)))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range seg.List {
			part, err := os.ReadFile(filepath.Join(out, "media", filepath.FromSlash(s.Src)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(part[4:], []byte("moof")) {
				t.Errorf("%s: %s doesn't start at a fragment", enc.Label, s.Src)
			}
			joined = append(joined, part...)
		}
		whole := filepath.Join(t.TempDir(), "joined.mp4")
		if err := os.WriteFile(whole, joined, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err = StreamHash(ctx, whole); err != nil || got != want {
			t.Errorf("%s: joined segments stream %s, source %s (%v)", enc.Label, got, want, err)
		}

		// every segment opens on a key frame, and B-frames give the timeline an offset
		if seg.Start <= 0 {
			t.Errorf("%s: start = %v, want the reorder delay", enc.Label, seg.Start)
		}
		var total float64
		for _, s := range seg.List {
			total += s.Duration
		}
		if total < 3.9 || total > 4.1 {
			t.Errorf("%s: segments add up to %v s, want 4", enc.Label, total)
		}
	}
}
