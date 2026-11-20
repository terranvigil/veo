// Package compare builds and serves a browser player that compares encodes
// of one source, with per-frame quality curves and jump points.
package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

// ManifestVersion is bumped when a field changes meaning or goes away.
const ManifestVersion = 1

// ffprobe's codec names
const (
	codecHEVC = "hevc"
	codecH264 = "h264"
)

// MaxEncodes is how many encodes one comparison can hold. The player shows
// two at a time and lets the viewer pick which.
const MaxEncodes = 4

// Manifest is everything the player reads. The static export writes it as
// manifest.json next to index.html, and the local server serves the same file.
type Manifest struct {
	Version  int     `json:"version"`
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle,omitempty"`
	FPS      float64 `json:"fps"`
	Frames   int     `json:"frames"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Metric   Metric  `json:"metric"`
	// MediaBase is what every src, init and playlist below is relative to.
	// Point it at another host to move the videos without touching the page.
	MediaBase string `json:"mediaBase"`
	// Variants link the same comparison at other resolutions, each its own
	// export. The player loads another one only when the viewer picks it.
	Variants  []Variant `json:"variants,omitempty"`
	Encodes   []*Stream `json:"encodes"`
	Reference *Stream   `json:"reference,omitempty"`
	Compare   Compare   `json:"compare"`
	// Moments holds the jump points of each pair of encodes under "idA|idB",
	// and of a lone encode under its id.
	Moments map[string][]Moment `json:"moments"`
	Notes   *Notes              `json:"notes,omitempty"`
}

type Metric struct {
	Key   string `json:"key"`             // name under frames[].metrics in the libvmaf log
	Label string `json:"label"`           // what the player prints, e.g. "VMAF"
	Model string `json:"model,omitempty"` // e.g. "vmaf_v0.6.1"
}

// Variant is one resolution of a title. Href is empty for the page itself.
type Variant struct {
	Label string `json:"label"`
	Href  string `json:"href,omitempty"`
}

// Compare names the encodes the player opens with on its A and B sides.
type Compare struct {
	A string `json:"a"`
	B string `json:"b,omitempty"`
}

type Stream struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Codec  string `json:"codec,omitempty"`  // display name, e.g. "HEVC Main"
	Detail string `json:"detail,omitempty"` // free text, e.g. "medium, CRF 25"
	// Src is the whole encode as one progressive file: the download, and what
	// plays where segments can't. Empty when only segments are hosted.
	Src         string    `json:"src,omitempty"`
	Mime        string    `json:"mime,omitempty"` // with codecs=, so the browser can say no before loading
	Segments    *Segments `json:"segments,omitempty"`
	Fallback    *Source   `json:"fallback,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	BitrateKbps float64   `json:"bitrateKbps,omitempty"`
	Score       *Stats    `json:"score,omitempty"`
	Scores      Scores    `json:"scores,omitempty"` // one value per frame
}

// Source is a second file a browser can play when it can't decode the first.
type Source struct {
	Src  string `json:"src"`
	Mime string `json:"mime,omitempty"`
}

type Stats struct {
	Mean float64 `json:"mean"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	P5   float64 `json:"p5"` // 5th percentile: the level the worst frames sit at
}

// Moment kinds.
const (
	KindDip  = "dip"  // a low point of one encode's own curve
	KindLead = "lead" // one encode furthest ahead of the other
)

// Moment is one place worth looking at. Side says which encode of the pair it
// is about: "a" is the first id in the pair's key. Rank 1 is the strongest of
// its kind and side.
type Moment struct {
	Kind  string  `json:"kind"`
	Side  string  `json:"side"`
	Rank  int     `json:"rank"`
	Frame int     `json:"frame"`
	Time  float64 `json:"time"`
}

// Notes fills the "how this was made" panel.
type Notes struct {
	Heading  string    `json:"heading,omitempty"`
	Body     []string  `json:"body,omitempty"` // paragraphs
	Commands []Command `json:"commands,omitempty"`
	Links    []Link    `json:"links,omitempty"`
}

type Command struct {
	Label  string `json:"label"`
	Text   string `json:"text"`
	Encode string `json:"encode,omitempty"` // an encode id or "reference": files the command under it
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Scores marshals with two decimals, which keeps a ten-minute manifest small.
type Scores []float64

func (s Scores) MarshalJSON() ([]byte, error) {
	out := make([]byte, 0, len(s)*6+2)
	out = append(out, '[')
	for i, v := range s {
		if i > 0 {
			out = append(out, ',')
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		out = strconv.AppendFloat(out, round(v, 2), 'f', -1, 64)
	}
	return append(out, ']'), nil
}

// Input is one video and what is known about it.
type Input struct {
	Path     string
	Scores   string // per-frame quality JSON; empty for none
	Label    string
	Detail   string
	Fallback string // a file browsers without the codec can play instead
}

// Options describe one comparison.
type Options struct {
	// Encodes holds up to MaxEncodes slots, ids "a" to "d" by position. Slots
	// with no Path are skipped. The first two filled open on the A and B sides.
	Encodes [MaxEncodes]Input
	Ref     Input
	// RefURL names a reference hosted somewhere else, instead of Ref.Path.
	// Nothing is copied for it.
	RefURL string

	MediaBase     string    // manifest mediaBase; default "media/"
	Variants      []Variant // other resolutions of this title
	NoSegments    bool      // export: don't cut fragmented MP4
	NoProgressive bool      // export: host only the segments
	Package       Packager  // export: nil uses ffmpeg

	Title     string
	Subtitle  string
	FPS       float64 // overrides the probed rate
	MetricKey string  // default "vmaf"
	Metric    string  // display label; default from the key
	Model     string
	NotesFile string // .json in the Notes shape, or text with blank-line paragraphs
	PerKind   int    // moments per kind and side; default 4

	// Probe reads a file's video properties. Nil uses ffprobe.
	Probe func(ctx context.Context, path string) (*MediaInfo, error)
}

// MediaInfo is the part of a probe the manifest needs.
type MediaInfo struct {
	Codec   string // ffprobe codec_name
	Tag     string // sample entry, e.g. "hvc1"
	Profile string
	PixFmt  string
	Level   int
	Width   int
	Height  int
	FPS     float64
	Frames  int
}

// File maps a manifest src to the file on disk it stands for. Stream is set
// for a stream's own video and nil for its fallback.
type File struct {
	Src    string
	Path   string
	Stream *Stream
}

// Build reads the inputs and returns the manifest with the files it names.
func Build(ctx context.Context, opts *Options) (*Manifest, []File, error) {
	metricKey := opts.MetricKey
	if metricKey == "" {
		metricKey = "vmaf"
	}
	probe := opts.Probe
	if probe == nil {
		probe = ffprobe
	}

	m := &Manifest{
		Version:   ManifestVersion,
		Title:     opts.Title,
		FPS:       opts.FPS,
		Metric:    Metric{Key: metricKey, Label: opts.Metric, Model: opts.Model},
		MediaBase: opts.MediaBase,
		Variants:  opts.Variants,
		Encodes:   []*Stream{},
		Moments:   map[string][]Moment{},
	}
	if m.MediaBase == "" {
		m.MediaBase = "media/"
	}
	if !strings.HasSuffix(m.MediaBase, "/") {
		m.MediaBase += "/"
	}
	if m.Metric.Label == "" {
		m.Metric.Label = metricLabel(metricKey)
	}

	var files []File
	add := func(id string, in Input, fallbackLabel string) (*Stream, error) {
		st, err := os.Stat(in.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		s := &Stream{
			ID:     id,
			Label:  in.Label,
			Detail: in.Detail,
			Src:    id + strings.ToLower(filepath.Ext(in.Path)),
			Mime:   containerType(in.Path),
			Bytes:  st.Size(),
		}
		if s.Label == "" {
			s.Label = fallbackLabel
		}
		files = append(files, File{Src: s.Src, Path: in.Path, Stream: s})

		info, err := probe(ctx, in.Path)
		if err != nil {
			slog.Warn("could not probe video; pass --fps if the player's timing is off", "file", in.Path, "error", err)
		} else {
			s.Codec = CodecLabel(info)
			s.Mime = MimeType(in.Path, info)
			if m.FPS == 0 {
				m.FPS = info.FPS
			}
			if m.Width == 0 {
				m.Width, m.Height = info.Width, info.Height
			}
			m.Frames = max(m.Frames, info.Frames)
			if info.Codec == codecHEVC && info.Tag != "" && info.Tag != "hvc1" {
				slog.Warn("HEVC is not tagged hvc1, so Safari won't play it; remux with -tag:v hvc1", "file", in.Path, "tag", info.Tag)
			}
		}

		if in.Fallback != "" {
			if _, err := os.Stat(in.Fallback); err != nil {
				return nil, fmt.Errorf("%s fallback: %w", id, err)
			}
			fb := &Source{
				Src:  id + "-fallback" + strings.ToLower(filepath.Ext(in.Fallback)),
				Mime: containerType(in.Fallback),
			}
			if fi, err := probe(ctx, in.Fallback); err == nil {
				fb.Mime = MimeType(in.Fallback, fi)
			}
			s.Fallback = fb
			files = append(files, File{Src: fb.Src, Path: in.Fallback})
		}

		if in.Scores != "" {
			scores, err := LoadScores(in.Scores, metricKey)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			s.Scores = scores
			s.Score = Summarize(scores)
			m.Frames = max(m.Frames, len(scores))
		}
		return s, nil
	}

	for i, in := range opts.Encodes {
		if in.Path == "" {
			continue
		}
		id := string(rune('a' + i))
		s, err := add(id, in, strings.ToUpper(id))
		if err != nil {
			return nil, nil, err
		}
		m.Encodes = append(m.Encodes, s)
	}
	if len(m.Encodes) == 0 {
		return nil, nil, errors.New("at least one encode is required")
	}
	m.Compare.A = m.Encodes[0].ID
	if len(m.Encodes) > 1 {
		m.Compare.B = m.Encodes[1].ID
	}
	switch {
	case opts.Ref.Path != "":
		s, err := add("reference", opts.Ref, "Source")
		if err != nil {
			return nil, nil, err
		}
		m.Reference = s
	case opts.RefURL != "":
		label := opts.Ref.Label
		if label == "" {
			label = "Source"
		}
		m.Reference = &Stream{ID: "reference", Label: label, Detail: opts.Ref.Detail, Src: opts.RefURL, Mime: containerType(opts.RefURL)}
	}
	if m.Title == "" {
		first := files[0].Path
		m.Title = strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
	}

	if m.FPS <= 0 {
		slog.Warn("frame rate unknown, assuming 24; pass --fps to set it")
		m.FPS = 24
	}
	m.FPS = round(m.FPS, 4)

	all := m.Encodes
	if m.Reference != nil {
		all = append(all[:len(all):len(all)], m.Reference)
	}
	for _, s := range all {
		if m.Frames > 0 && s.Bytes > 0 {
			s.BitrateKbps = round(float64(s.Bytes)*8/1000/(float64(m.Frames)/m.FPS), 1)
		}
	}

	perKind := opts.PerKind
	if perKind <= 0 {
		perKind = 4
	}
	for i, a := range m.Encodes {
		if len(a.Scores) == 0 {
			continue
		}
		if len(m.Encodes) == 1 {
			m.Moments[a.ID] = FindMoments(a.Scores, nil, m.FPS, perKind)
		}
		for _, b := range m.Encodes[i+1:] {
			if len(b.Scores) == 0 {
				continue
			}
			if len(a.Scores) != len(b.Scores) {
				slog.Warn("two quality logs differ in length; comparing the frames both have",
					a.Label, len(a.Scores), b.Label, len(b.Scores))
			}
			m.Moments[a.ID+"|"+b.ID] = FindMoments(a.Scores, b.Scores, m.FPS, perKind)
		}
	}

	if opts.NotesFile != "" {
		notes, err := LoadNotes(opts.NotesFile)
		if err != nil {
			return nil, nil, err
		}
		m.Notes = notes
	}
	m.Subtitle = opts.Subtitle
	m.scrub()
	return m, files, nil
}

func metricLabel(key string) string {
	if strings.HasPrefix(strings.ToLower(key), "vmaf") {
		return "VMAF"
	}
	return key
}

// LoadNotes reads the "how this was made" text. A .json file is the Notes
// shape; anything else is plain text, one paragraph per blank-line block.
func LoadNotes(path string) (*Notes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read notes: %w", err)
	}
	n := &Notes{}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		if err := json.Unmarshal(data, n); err != nil {
			return nil, fmt.Errorf("failed to parse notes %s: %w", path, err)
		}
		return n, nil
	}
	for block := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n") {
		if p := strings.Join(strings.Fields(block), " "); p != "" {
			n.Body = append(n.Body, p)
		}
	}
	return n, nil
}

func ffprobe(ctx context.Context, path string) (*MediaInfo, error) {
	res, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		return nil, err
	}
	v := res.VideoStream()
	if v == nil {
		return nil, fmt.Errorf("no video stream in %s", path)
	}
	info := &MediaInfo{
		Codec:   v.CodecName,
		Tag:     v.CodecTag,
		Profile: v.Profile,
		PixFmt:  v.PixFmt,
		Level:   v.Level,
		Width:   v.Width,
		Height:  v.Height,
		FPS:     v.FPS(),
		Frames:  v.NbFrames,
	}
	if info.Frames == 0 && info.FPS > 0 {
		info.Frames = int(math.Round(res.Format.Duration * info.FPS))
	}
	return info, nil
}

func containerType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	}
	return "video/mp4"
}

func is10Bit(info *MediaInfo) bool {
	return strings.Contains(info.PixFmt, "10") || strings.Contains(info.Profile, "10")
}

// MimeType returns the container type with an RFC 6381 codecs parameter, which
// is what lets a browser refuse HEVC before it downloads anything.
func MimeType(path string, info *MediaInfo) string {
	base := containerType(path)
	var codecs string
	switch info.Codec {
	case codecHEVC:
		tag := "hvc1"
		if info.Tag == "hev1" {
			tag = "hev1"
		}
		level := info.Level
		if level <= 0 {
			level = 120
		}
		profile := "1.6"
		if is10Bit(info) {
			profile = "2.4"
		}
		codecs = fmt.Sprintf("%s.%s.L%d.B0", tag, profile, level)
	case codecH264:
		idc := map[string]string{
			"Constrained Baseline": "42E0", "Baseline": "4200", "Main": "4D40",
			"High": "6400", "High 10": "6E00", "High 4:2:2": "7A00", "High 4:4:4 Predictive": "F400",
		}[info.Profile]
		if idc == "" {
			idc = "6400"
		}
		level := info.Level
		if level <= 0 {
			level = 40
		}
		codecs = fmt.Sprintf("avc1.%s%02X", idc, level)
	case "av1":
		depth := "08"
		if is10Bit(info) {
			depth = "10"
		}
		codecs = fmt.Sprintf("av01.0.%02dM.%s", max(info.Level, 0), depth)
	case "vp9":
		codecs = "vp09.00.40.08"
		if is10Bit(info) {
			codecs = "vp09.02.40.10"
		}
	case "vp8":
		codecs = "vp8"
	default:
		return base
	}
	return base + `; codecs="` + codecs + `"`
}

// CodecLabel is the short codec name the player prints beside a stream.
func CodecLabel(info *MediaInfo) string {
	name := map[string]string{
		codecHEVC: "HEVC", codecH264: "H.264", "av1": "AV1", "vp9": "VP9", "vp8": "VP8", "vvc": "VVC",
	}[info.Codec]
	if name == "" {
		name = strings.ToUpper(info.Codec)
	}
	if info.Profile != "" && info.Profile != "unknown" && (info.Codec == codecHEVC || info.Codec == codecH264) {
		name += " " + info.Profile
	}
	return name
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
