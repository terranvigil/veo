package compare

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

const (
	playlistName = "index.m3u8"
	initName     = "init.mp4"
)

// Segments is a stream cut into fragmented MP4 (CMAF): an init segment, one
// media segment per run of GOPs, and an HLS playlist over them. The frames
// are the progressive file's, byte for byte.
type Segments struct {
	Playlist string `json:"playlist"` // HLS media playlist, for browsers that play HLS themselves
	Init     string `json:"init"`
	// Start is the presentation time of the first frame on the segments'
	// timeline. Fragmented MP4 can't hold the negative decode times a
	// progressive file uses for B-frames, so the muxer shifts the stream, and
	// two encoders with different reorder depths shift by different amounts.
	// The player subtracts it, which is what keeps frame N of one encode
	// beside frame N of the other.
	Start float64   `json:"start"`
	List  []Segment `json:"list"`
}

type Segment struct {
	Src      string  `json:"src"`
	Start    float64 `json:"start"` // seconds from the first frame
	Duration float64 `json:"duration"`
	Bytes    int64   `json:"bytes"`
}

// Packager cuts the video at in into dir as index.m3u8, init.mp4 and media
// segments, without re-encoding, and returns the first frame's presentation
// time on the new timeline.
type Packager func(ctx context.Context, in, dir string) (start float64, err error)

// segmentable says whether a file is a container ffmpeg can re-cut as CMAF.
func segmentable(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".mp4", ".m4v", ".mov":
		return true
	}
	return false
}

// readSegments lists what a packager left in dir. src is dir's path as the
// manifest names it.
func readSegments(dir, src string, start float64) (*Segments, error) {
	data, err := os.ReadFile(filepath.Join(dir, playlistName))
	if err != nil {
		return nil, fmt.Errorf("failed to read playlist: %w", err)
	}
	initFile, list, err := ParsePlaylist(data)
	if err != nil {
		return nil, err
	}
	out := &Segments{
		Playlist: path.Join(src, playlistName),
		Init:     path.Join(src, initFile),
		Start:    round(start, 6),
	}
	var at float64
	for _, seg := range list {
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(seg.Src)))
		if err != nil {
			return nil, fmt.Errorf("playlist names a missing segment: %w", err)
		}
		out.List = append(out.List, Segment{
			Src:      path.Join(src, seg.Src),
			Start:    round(at, 6),
			Duration: seg.Duration,
			Bytes:    st.Size(),
		})
		at += seg.Duration
	}
	return out, nil
}

// ParsePlaylist reads an HLS media playlist of fragmented MP4: the init
// segment's URI and each media segment with its duration.
func ParsePlaylist(data []byte) (initFile string, list []Segment, err error) {
	var duration float64
	haveDuration := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			_, rest, ok := strings.Cut(line, `URI="`)
			if !ok {
				return "", nil, fmt.Errorf("playlist has an EXT-X-MAP with no URI: %s", line)
			}
			initFile, _, _ = strings.Cut(rest, `"`)
		case strings.HasPrefix(line, "#EXTINF:"):
			value, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			duration, err = strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return "", nil, fmt.Errorf("playlist has a bad EXTINF: %s", line)
			}
			haveDuration = true
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if !haveDuration {
				return "", nil, fmt.Errorf("playlist segment %s has no EXTINF", line)
			}
			list = append(list, Segment{Src: line, Duration: duration})
			haveDuration = false
		}
	}
	if initFile == "" {
		return "", nil, fmt.Errorf("playlist has no EXT-X-MAP, so it isn't fragmented MP4")
	}
	if len(list) == 0 {
		return "", nil, fmt.Errorf("playlist has no segments")
	}
	return initFile, list, nil
}

// PackageFFmpeg is the default Packager. It remuxes with -c copy into
// fragments that start only at key frames the encoder already placed, splits
// the fragments into files, then proves the packaged frames are the source's
// by hashing both elementary streams.
func PackageFFmpeg(ctx context.Context, in, dir string) (float64, error) {
	args := []string{
		"-v", "error", "-y", "-i", in,
		"-map", "0:v:0", "-c", "copy", "-an",
	}
	probe, err := ffmpeg.Probe(ctx, in)
	if err != nil {
		return 0, err
	}
	if v := probe.VideoStream(); v != nil && v.CodecName == codecHEVC {
		// Safari only plays HEVC under this sample entry
		args = append(args, "-tag:v", "hvc1")
	}
	// one fragment per key frame the encoder placed, joined up to two seconds
	whole := filepath.Join(dir, "whole.frag.mp4")
	args = append(args,
		"-f", "mp4",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-min_frag_duration", "2000000",
		whole,
	)
	defer func() { _ = os.Remove(whole) }()
	if out, err := exec.CommandContext(ctx, ffmpeg.FFmpegPath(), args...).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("ffmpeg could not fragment %s: %w\n%s", filepath.Base(in), err, out)
	}
	f, err := os.Open(whole)
	if err != nil {
		return 0, err
	}
	start, err := SplitFragments(f, dir)
	_ = f.Close()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", filepath.Base(in), err)
	}
	if err := os.Remove(whole); err != nil {
		return 0, err
	}

	playlist := filepath.Join(dir, playlistName)
	want, err := StreamHash(ctx, in)
	if err != nil {
		return 0, err
	}
	got, err := StreamHash(ctx, playlist)
	if err != nil {
		return 0, err
	}
	if want != got {
		return 0, fmt.Errorf("segments of %s don't hold the same bitstream as the file (%s, want %s)", filepath.Base(in), got, want)
	}

	return start, nil
}

// StreamHash returns a SHA-256 of a file's video elementary stream, with the
// container taken away. HEVC and H.264 are hashed as Annex B.
func StreamHash(ctx context.Context, file string) (string, error) {
	probe, err := ffmpeg.Probe(ctx, file)
	if err != nil {
		return "", err
	}
	args := []string{"-v", "error", "-i", file, "-map", "0:v:0", "-c", "copy"}
	if v := probe.VideoStream(); v != nil {
		switch v.CodecName {
		case codecHEVC:
			args = append(args, "-bsf:v", "hevc_mp4toannexb")
		case codecH264:
			args = append(args, "-bsf:v", "h264_mp4toannexb")
		}
	}
	args = append(args, "-f", "hash", "-hash", "sha256", "-")
	cmd := exec.CommandContext(ctx, ffmpeg.FFmpegPath(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffmpeg could not hash %s: %w\n%s", filepath.Base(file), err, stderr.String())
	}
	sum := strings.TrimSpace(string(out))
	if !strings.HasPrefix(sum, "SHA256=") {
		return "", fmt.Errorf("unexpected hash output for %s: %q", filepath.Base(file), sum)
	}
	return sum, nil
}
