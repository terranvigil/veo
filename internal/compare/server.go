package compare

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/terranvigil/veo/web"
)

const manifestName = "manifest.json"

// NewHandler serves the player, its manifest and the videos under the same
// relative URLs the static export uses, so one page works in both.
func NewHandler(m *Manifest, files []File) (http.Handler, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to encode manifest: %w", err)
	}
	paths := make(map[string]string, len(files))
	for _, f := range files {
		paths["/media/"+f.Src] = f.Path
	}
	static := http.FileServerFS(web.Static())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+manifestName {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(body)
			return
		}
		if path, ok := paths[r.URL.Path]; ok {
			// ServeFile answers range requests, which seeking needs
			http.ServeFile(w, r, path)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		static.ServeHTTP(w, r)
	}), nil
}

// Serve runs the player on localhost until ctx ends. It cuts the videos into
// segments in a temporary folder first, as the export does, because browsers
// seek segments to the exact frame and whole files only nearly. Without
// ffmpeg it serves the files as they are.
func Serve(ctx context.Context, opts *Options, port int, open bool) error {
	if !opts.NoSegments {
		tmp, err := os.MkdirTemp("", "veo-compare-")
		if err != nil {
			return fmt.Errorf("failed to create a temporary folder: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		local := *opts
		local.MediaBase = ""
		local.NoProgressive = true
		res, err := Export(ctx, &local, tmp)
		if err == nil {
			PrintSummary(res.Manifest)
			return Preview(ctx, tmp, port, open)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("could not segment the videos, serving them whole; seeks may land a few frames off", "error", err)
	}
	// the local server always serves its own files, whole, over range requests
	local := *opts
	local.MediaBase = ""
	m, files, err := Build(ctx, &local)
	if err != nil {
		return err
	}
	handler, err := NewHandler(m, files)
	if err != nil {
		return err
	}

	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		// the asked-for port is taken, so let the system pick one
		listener, err = lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("failed to start server: %w", err)
		}
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return errors.New("failed to start server: no TCP address")
	}

	url := fmt.Sprintf("http://localhost:%d", addr.Port)
	fmt.Printf("Comparison player: %s\n", url)
	PrintSummary(m)
	fmt.Println("\nPress Ctrl+C to stop")
	if open {
		openBrowser(ctx, url)
	}

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Exported is what Export wrote.
type Exported struct {
	Manifest *Manifest
	Bytes    int64 // everything in the folder
}

// Export writes a folder any static host can serve: the player, the manifest
// and each video, both whole and cut into fragmented MP4 with an HLS playlist.
func Export(ctx context.Context, opts *Options, dir string) (*Exported, error) {
	m, files, err := Build(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", dir, err)
	}

	static := web.Static()
	err = fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		in, err := static.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		return writeFile(filepath.Join(dir, filepath.FromSlash(path)), in)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to write player: %w", err)
	}

	pack := opts.Package
	if pack == nil {
		pack = PackageFFmpeg
	}
	media := filepath.Join(dir, "media")
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s := f.Stream
		if s != nil && !opts.NoSegments && segmentable(f.Path) {
			segDir := filepath.Join(media, s.ID)
			// stale segments from an earlier export would sit beside the new ones
			if err := os.RemoveAll(segDir); err != nil {
				return nil, fmt.Errorf("failed to clear %s: %w", segDir, err)
			}
			if err := os.MkdirAll(segDir, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create %s: %w", segDir, err)
			}
			start, err := pack(ctx, f.Path, segDir)
			if err != nil {
				return nil, err
			}
			if s.Segments, err = readSegments(segDir, s.ID, start); err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Base(f.Path), err)
			}
		}
		whole := filepath.Join(media, filepath.FromSlash(f.Src))
		if s != nil && s.Segments != nil && (opts.NoProgressive || s.Bytes > pagesLimit) {
			if !opts.NoProgressive {
				slog.Warn("file is over 100 MB, the most GitHub Pages serves, so only its segments are exported", "file", filepath.Base(f.Path))
			}
			s.Src = ""
			if err := os.Remove(whole); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			continue
		}
		checkStreamable(f.Path)
		in, err := os.Open(f.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to copy video: %w", err)
		}
		err = writeFile(whole, in)
		_ = in.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to copy %s: %w", f.Path, err)
		}
	}

	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to encode manifest: %w", err)
	}
	if err := checkPrivate(body); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), append(body, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}
	// stops GitHub Pages from running the folder through Jekyll
	if err := os.WriteFile(filepath.Join(dir, ".nojekyll"), nil, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write .nojekyll: %w", err)
	}
	total, err := DirSize(dir)
	if err != nil {
		return nil, err
	}
	return &Exported{Manifest: m, Bytes: total}, nil
}

// DirSize adds up every file under dir.
func DirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// pagesLimit is the largest file GitHub Pages will serve from a repository.
const pagesLimit = 100 << 20

// checkStreamable warns about a video a static host would serve badly.
func checkStreamable(path string) {
	if st, err := os.Stat(path); err == nil && st.Size() > pagesLimit {
		slog.Warn("file is over 100 MB, the most GitHub Pages serves", "file", filepath.Base(path), "size", FormatBytes(st.Size()))
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if fast, err := FastStart(f); err == nil && !fast {
		slog.Warn("MP4 index is at the end of the file, so a browser must fetch all of it before playing; remux with -movflags +faststart", "file", filepath.Base(path))
	}
}

// FastStart reports whether an MP4's moov box comes before its media data,
// which is what lets a browser start and seek over range requests. A file
// that isn't an MP4 returns an error.
func FastStart(r io.ReadSeeker) (bool, error) {
	var head [16]byte
	for boxes := 0; boxes < 64; boxes++ {
		if _, err := io.ReadFull(r, head[:8]); err != nil {
			return false, fmt.Errorf("no moov box found: %w", err)
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		kind := string(head[4:8])
		used := int64(8)
		if size == 1 {
			if _, err := io.ReadFull(r, head[8:16]); err != nil {
				return false, err
			}
			size = int64(binary.BigEndian.Uint64(head[8:16]))
			used = 16
		}
		switch kind {
		case boxMoov:
			return true, nil
		case "mdat":
			return false, nil
		}
		if boxes == 0 && kind != "ftyp" && kind != "styp" {
			return false, errors.New("not an MP4 file")
		}
		if size < used {
			return false, errors.New("no moov box found")
		}
		if _, err := r.Seek(size-used, io.SeekCurrent); err != nil {
			return false, err
		}
	}
	return false, errors.New("no moov box found")
}

func writeFile(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// PrintSummary writes one line per stream to stdout.
func PrintSummary(m *Manifest) {
	row := func(id string, s *Stream) {
		line := fmt.Sprintf("  %-4s %s", id, s.Label)
		if s.Codec != "" {
			line += ", " + s.Codec
		}
		if s.Bytes > 0 {
			line += fmt.Sprintf(", %s", FormatBytes(s.Bytes))
		}
		if s.Score != nil {
			line += fmt.Sprintf(", mean %s %.2f", m.Metric.Label, s.Score.Mean)
		}
		fmt.Println(line)
	}
	for _, s := range m.Encodes {
		row(strings.ToUpper(s.ID), s)
	}
	if m.Reference != nil {
		row("ref", m.Reference)
	}
	if key := m.Compare.A + "|" + m.Compare.B; len(m.Moments[key]) > 0 {
		fmt.Printf("  %d jump points between A and B\n", len(m.Moments[key]))
	}
}

func FormatBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.2f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1f kB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

func openBrowser(ctx context.Context, url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", strings.ReplaceAll(url, "&", "^&"))
	default:
		return
	}
	_ = cmd.Start()
}
