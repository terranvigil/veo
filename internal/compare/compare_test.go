//go:build unit

package compare

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const libvmafLog = `{
  "version": "3.0.0",
  "frames": [
    {"frameNum": 0, "metrics": {"vmaf": 90.123456, "psnr_y": 40.1}},
    {"frameNum": 1, "metrics": {"vmaf": 80.5, "psnr_y": 39.0}},
    {"frameNum": 2, "metrics": {"vmaf": 70, "psnr_y": 38.0}}
  ],
  "pooled_metrics": {"vmaf": {"mean": 80.2}}
}`

func TestParseScoresLibvmaf(t *testing.T) {
	got, err := ParseScores([]byte(libvmafLog), "vmaf")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{90.123456, 80.5, 70}
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestParseScoresNamedModel(t *testing.T) {
	// a model loaded with name=v1 writes its score under that name
	log := `{"frames":[{"frameNum":0,"metrics":{"integer_adm2":0.9,"v1":88}},{"frameNum":1,"metrics":{"integer_adm2":0.8,"v1":77}}]}`
	got, err := ParseScores([]byte(log), "v1")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 88 || got[1] != 77 {
		t.Errorf("got %v", got)
	}

	// "vmaf" finds a lone vmaf_* model, and says what is there when it can't
	log = `{"frames":[{"frameNum":0,"metrics":{"vmaf_v1":61,"psnr_y":30}}]}`
	if got, err = ParseScores([]byte(log), "vmaf"); err != nil || got[0] != 61 {
		t.Errorf("prefix match: got %v, %v", got, err)
	}
	_, err = ParseScores([]byte(log), "ssim")
	if err == nil || !strings.Contains(err.Error(), "psnr_y, vmaf_v1") {
		t.Errorf("want an error naming the metrics present, got %v", err)
	}
}

func TestParseScoresVeoResultAndGaps(t *testing.T) {
	// veo quality measure output, subsampled every third frame
	log := `{"vmaf": 80, "frames": [{"frameNum": 0, "vmaf": 90}, {"frameNum": 3, "vmaf": 60}, {"frameNum": 6, "vmaf": 90}]}`
	got, err := ParseScores([]byte(log), "vmaf")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{90, 80, 70, 60, 70, 80, 90}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if d := got[i] - want[i]; d > 1e-9 || d < -1e-9 {
			t.Errorf("frame %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestParseScoresErrors(t *testing.T) {
	for name, log := range map[string]string{
		"not json":  `nope`,
		"no frames": `{"frames": []}`,
		"no metric": `{"frames":[{"frameNum":0}]}`,
	} {
		if _, err := ParseScores([]byte(log), "vmaf"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSummarize(t *testing.T) {
	if Summarize(nil) != nil {
		t.Error("empty series should have no stats")
	}
	v := make([]float64, 101)
	for i := range v {
		v[i] = float64(i)
	}
	s := Summarize(v)
	if s.Mean != 50 || s.Min != 0 || s.Max != 100 || s.P5 != 5 {
		t.Errorf("got %+v", s)
	}
}

func TestScoresMarshalRounds(t *testing.T) {
	out, err := json.Marshal(Scores{90.123456, 80, 79.999})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[90.12,80,80]" {
		t.Errorf("got %s", out)
	}
}

// flat returns n frames at level, with [from, to) set to dip.
func flat(n int, level float64, from, to int, dip float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = level
		if i >= from && i < to {
			v[i] = dip
		}
	}
	return v
}

func find(ms []Moment, kind, side string) []Moment {
	var out []Moment
	for _, m := range ms {
		if m.Kind == kind && m.Side == side {
			out = append(out, m)
		}
	}
	return out
}

func TestFindMomentsPair(t *testing.T) {
	const fps = 24.0
	a := flat(480, 95, 100, 124, 70) // A dips at 100
	b := flat(480, 95, 300, 324, 60) // B dips at 300
	a[110] = 65                      // the single worst frame of A's dip

	ms := FindMoments(a, b, fps, 3)

	dipA := find(ms, KindDip, "a")
	if len(dipA) == 0 || dipA[0].Rank != 1 || dipA[0].Frame != 110 {
		t.Errorf("A's worst dip should land on its lowest frame 110, got %+v", dipA)
	}
	dipB := find(ms, KindDip, "b")
	if len(dipB) == 0 || dipB[0].Frame < 300 || dipB[0].Frame >= 324 {
		t.Errorf("B's worst dip should be inside 300-323, got %+v", dipB)
	}
	// where A dips, B is ahead, and the other way round
	leadB := find(ms, KindLead, "b")
	if len(leadB) != 1 || leadB[0].Frame != 110 {
		t.Errorf("B leads most at frame 110, got %+v", leadB)
	}
	leadA := find(ms, KindLead, "a")
	if len(leadA) != 1 || leadA[0].Frame < 300 || leadA[0].Frame >= 324 {
		t.Errorf("A leads inside 300-323, got %+v", leadA)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].Frame < ms[i-1].Frame {
			t.Fatalf("moments are not in frame order: %+v", ms)
		}
	}
	if got := dipA[0].Time; got != round(110/fps, 3) {
		t.Errorf("time = %v", got)
	}
}

func TestFindMomentsSpacingAndLimits(t *testing.T) {
	// one long trough must give one pick, not perKind neighbors
	a := flat(480, 95, 200, 230, 60)
	dips := find(FindMoments(a, nil, 24, 4), KindDip, "a")
	if len(dips) != 1 {
		t.Errorf("want one dip for one trough, got %+v", dips)
	}

	// level curves still list the single worst stretch, and no leads
	level := flat(240, 90, 0, 0, 0)
	ms := FindMoments(level, level, 24, 4)
	if len(find(ms, KindDip, "a")) != 1 || len(find(ms, KindLead, "a"))+len(find(ms, KindLead, "b")) != 0 {
		t.Errorf("level curves: got %+v", ms)
	}

	// unequal logs compare the frames both have
	ms = FindMoments(flat(100, 90, 0, 0, 0), flat(50, 90, 40, 50, 50), 24, 2)
	for _, m := range ms {
		if m.Frame >= 50 {
			t.Errorf("moment past the shorter log: %+v", m)
		}
	}

	if got := FindMoments(nil, nil, 24, 4); got == nil || len(got) != 0 {
		t.Errorf("no scores should give an empty, non-nil list, got %#v", got)
	}
}

func TestMimeTypeAndCodecLabel(t *testing.T) {
	cases := []struct {
		path  string
		info  MediaInfo
		mime  string
		label string
	}{
		{"a.mp4", MediaInfo{Codec: "hevc", Tag: "hvc1", Profile: "Main", PixFmt: "yuv420p", Level: 120},
			`video/mp4; codecs="hvc1.1.6.L120.B0"`, "HEVC Main"},
		{"a.mp4", MediaInfo{Codec: "hevc", Tag: "hev1", Profile: "Main 10", PixFmt: "yuv420p10le", Level: 153},
			`video/mp4; codecs="hev1.2.4.L153.B0"`, "HEVC Main 10"},
		{"a.mp4", MediaInfo{Codec: "h264", Profile: "High", PixFmt: "yuv420p", Level: 40},
			`video/mp4; codecs="avc1.640028"`, "H.264 High"},
		{"a.mp4", MediaInfo{Codec: "av1", Profile: "Main", PixFmt: "yuv420p10le", Level: 8},
			`video/mp4; codecs="av01.0.08M.10"`, "AV1"},
		{"a.webm", MediaInfo{Codec: "vp9", PixFmt: "yuv420p"}, `video/webm; codecs="vp09.00.40.08"`, "VP9"},
		{"a.mp4", MediaInfo{Codec: "prores"}, "video/mp4", "PRORES"},
	}
	for _, c := range cases {
		if got := MimeType(c.path, &c.info); got != c.mime {
			t.Errorf("MimeType(%s %s) = %s, want %s", c.info.Codec, c.info.Profile, got, c.mime)
		}
		if got := CodecLabel(&c.info); got != c.label {
			t.Errorf("CodecLabel(%s) = %s, want %s", c.info.Codec, got, c.label)
		}
	}
}

func TestScrubPaths(t *testing.T) {
	cases := map[string]string{
		"/Users/me/src/build/ffmpeg -i /Volumes/T7/media/long form.mov -crf 25 out.mp4": "ffmpeg -i long form.mov -crf 25 out.mp4",
		"vmaf --model path=/Users/me/.local/share/vmaf/model/v1/vmaf_v1.json:name=v1":   "vmaf --model path=vmaf_v1.json:name=v1",
		`built from "/home/ci/work/enc" (/tmp/x/y.log)`:                                 `built from "enc" (y.log)`,
		"see https://example.com/Users/me/file and work/source.mkv":                     "see https://example.com/Users/me/file and work/source.mkv",
		"/Volumes/T7/":     "",
		"ffmpeg -i in.mov": "ffmpeg -i in.mov",
	}
	for in, want := range cases {
		if got := ScrubPaths(in); got != want {
			t.Errorf("ScrubPaths(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// bx builds one box.
func bx(kind string, body ...[]byte) []byte {
	payload := bytes.Join(body, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	return append(append(out, kind...), payload...)
}

func be32(v ...uint32) []byte {
	var out []byte
	for _, x := range v {
		out = binary.BigEndian.AppendUint32(out, x)
	}
	return out
}

// fragmented builds a fragmented MP4 at 24 units a second: one fragment per
// entry, each holding that many frames. Frames reorder by three, so the first
// presentation time is 3/24 s.
func fragmented(frames ...int) []byte {
	mdhd := bx("mdhd", be32(0, 0, 0, 24, 0, 0))
	out := append(bx("ftyp", []byte("isom")), bx("moov", bx("mvhd", be32(0)), bx("trak", bx("mdia", mdhd)))...)
	dts := uint32(0)
	for i, n := range frames {
		// trun: sample duration and composition offset present, plus the data offset
		trun := be32(0x00000901, uint32(n), 0)
		for s := 0; s < n; s++ {
			offset := uint32(3)
			if s%2 == 1 {
				offset = 5
			}
			trun = append(trun, be32(1, offset)...)
		}
		traf := bx("traf", bx("tfhd", be32(0x020000, 1)), bx("tfdt", be32(0, dts)), bx("trun", trun))
		out = append(out, bx("moof", bx("mfhd", be32(0, uint32(i+1))), traf)...)
		out = append(out, bx("mdat", bytes.Repeat([]byte{byte('A' + i)}, n*10))...)
		dts += uint32(n)
	}
	return append(out, bx("mfra", be32(0))...)
}

// fakePackage stands in for ffmpeg: two fragments, 2 s and 1 s.
func fakePackage(_ context.Context, _, dir string) (float64, error) {
	return SplitFragments(bytes.NewReader(fragmented(48, 24)), dir)
}

func TestSplitFragments(t *testing.T) {
	dir := t.TempDir()
	in := fragmented(48, 24, 12)
	start, err := SplitFragments(bytes.NewReader(in), dir)
	if err != nil {
		t.Fatal(err)
	}
	if start != 0.125 {
		t.Errorf("first presentation time = %v, want 0.125", start)
	}

	// nothing but regrouping: init plus segments is the input without its index
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	joined := bytes.Join([][]byte{read("init.mp4"), read("seg-00000.m4s"), read("seg-00001.m4s"), read("seg-00002.m4s")}, nil)
	if want := in[:len(in)-len(bx("mfra", be32(0)))]; !bytes.Equal(joined, want) {
		t.Errorf("init and segments are %d bytes, want the input's %d unchanged", len(joined), len(want))
	}
	if !bytes.HasPrefix(read("seg-00001.m4s")[4:], []byte("moof")) {
		t.Error("a segment should start at its moof")
	}

	initFile, list, err := ParsePlaylist(read("index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if initFile != "init.mp4" || len(list) != 3 {
		t.Fatalf("playlist: init %q, %d segments", initFile, len(list))
	}
	for i, want := range []float64{2, 1, 0.5} {
		if list[i].Duration != want || list[i].Src != fmt.Sprintf("seg-%05d.m4s", i) {
			t.Errorf("segment %d: %+v, want %v s", i, list[i], want)
		}
	}
	if pl := string(read("index.m3u8")); !strings.Contains(pl, "#EXT-X-TARGETDURATION:2\n") || !strings.HasSuffix(pl, "#EXT-X-ENDLIST\n") {
		t.Errorf("playlist:\n%s", pl)
	}

	if _, err := SplitFragments(bytes.NewReader(mp4("ftyp", "moov", "mdat")), t.TempDir()); err == nil {
		t.Error("want an error for an MP4 that isn't fragmented")
	}
	if _, err := SplitFragments(bytes.NewReader([]byte("short")), t.TempDir()); err == nil {
		t.Error("want an error for a truncated file")
	}
}

func TestParsePlaylistErrors(t *testing.T) {
	for name, pl := range map[string]string{
		"no map":      "#EXTM3U\n#EXTINF:2.0,\nseg.m4s\n",
		"no segments": "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n",
		"no extinf":   "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\nseg.m4s\n",
		"bad extinf":  "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:abc,\nseg.m4s\n",
	} {
		if _, _, err := ParsePlaylist([]byte(pl)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func mp4(boxes ...string) []byte {
	var out []byte
	for _, kind := range boxes {
		out = binary.BigEndian.AppendUint32(out, 16)
		out = append(out, kind...)
		out = append(out, make([]byte, 8)...)
	}
	return out
}

func TestFastStart(t *testing.T) {
	if fast, err := FastStart(bytes.NewReader(mp4("ftyp", "free", "moov", "mdat"))); err != nil || !fast {
		t.Errorf("moov first: got %v, %v", fast, err)
	}
	if fast, err := FastStart(bytes.NewReader(mp4("ftyp", "mdat", "moov"))); err != nil || fast {
		t.Errorf("mdat first: got %v, %v", fast, err)
	}
	if _, err := FastStart(bytes.NewReader([]byte("\x1aE\xdf\xa3 this is matroska, not mp4"))); err == nil {
		t.Error("want an error for a file that isn't MP4")
	}
	if _, err := FastStart(bytes.NewReader(mp4("ftyp", "free"))); err == nil {
		t.Error("want an error when there is no moov")
	}
}

// fixture writes three encodes, a reference and notes that are full of local paths.
func fixture(t *testing.T) *Options {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	log := func(name string, scores []float64) string {
		var b strings.Builder
		b.WriteString(`{"frames":[`)
		for i, s := range scores {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"frameNum":%d,"metrics":{"vmaf":%g,"psnr_y":40}}`, i, s)
		}
		b.WriteString(`]}`)
		return write(name, b.String())
	}
	notes := write("notes.json", `{
		"heading": "How this was made",
		"body": ["Source: /Volumes/T7/media/longform/tearsofsteel_4k.mov, cut on /Users/someone/box."],
		"commands": [
			{"label": "ours", "encode": "a", "text": "/Users/someone/src/build/ffmpeg -i /Volumes/T7/media/in.mov -crf 25 a.mp4"},
			{"label": "score", "text": "/Users/someone/.local/opt/vmaf/bin/vmaf --model path=/Users/someone/.local/share/vmaf/model/vmaf_v1.json -o out.json"}
		],
		"links": [{"label": "Film", "url": "https://mango.blender.org/"}]
	}`)

	opts := &Options{
		Package:   fakePackage,
		Title:     "Test title",
		Subtitle:  "cut from /Volumes/T7/media/in.mov",
		Model:     "/Users/someone/.local/share/vmaf/model/vmaf_v1.json",
		NotesFile: notes,
		PerKind:   2,
		Ref:       Input{Path: write("ref.mp4", string(mp4("ftyp", "moov", "mdat"))+"ref"), Label: "Source"},
		Probe: func(_ context.Context, path string) (*MediaInfo, error) {
			if strings.Contains(path, "ref") {
				return &MediaInfo{Codec: "h264", Tag: "avc1", Profile: "High", PixFmt: "yuv420p", Level: 40, Width: 352, Height: 288, FPS: 24, Frames: 240}, nil
			}
			return &MediaInfo{Codec: "hevc", Tag: "hvc1", Profile: "Main", PixFmt: "yuv420p", Level: 93, Width: 352, Height: 288, FPS: 24, Frames: 240}, nil
		},
	}
	opts.Encodes[0] = Input{
		Path: write("ours.mp4", strings.Repeat("a", 3000)), Scores: log("ours.json", flat(240, 95, 100, 124, 70)),
		Label: "ours", Detail: "built in /Users/someone/src/enc", Fallback: write("ours-h264.mp4", "proxy"),
	}
	opts.Encodes[1] = Input{
		Path: write("theirs.mp4", strings.Repeat("b", 3030)), Scores: log("theirs.json", flat(240, 93, 180, 204, 60)),
		Label: "theirs",
	}
	opts.Encodes[2] = Input{
		Path: write("theirs-grain.mp4", strings.Repeat("c", 2990)), Scores: log("grain.json", flat(240, 90, 30, 54, 75)),
		Label: "theirs, tuned",
	}
	return opts
}

func TestBuildManifest(t *testing.T) {
	m, files, err := Build(context.Background(), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != ManifestVersion || m.FPS != 24 || m.Frames != 240 || m.Width != 352 {
		t.Errorf("header: %+v", m)
	}
	if len(m.Encodes) != 3 || m.Reference == nil {
		t.Fatalf("want three encodes and a reference, got %d and %v", len(m.Encodes), m.Reference)
	}
	if m.Compare.A != "a" || m.Compare.B != "b" {
		t.Errorf("default sides: %+v", m.Compare)
	}

	a := m.Encodes[0]
	if m.MediaBase != "media/" {
		t.Errorf("mediaBase: %q", m.MediaBase)
	}
	if a.ID != "a" || a.Src != "a.mp4" || a.Bytes != 3000 || a.Codec != "HEVC Main" {
		t.Errorf("encode a: %+v", a)
	}
	if a.Mime != `video/mp4; codecs="hvc1.1.6.L93.B0"` {
		t.Errorf("mime: %s", a.Mime)
	}
	// 3000 bytes over 10 seconds
	if a.BitrateKbps != 2.4 {
		t.Errorf("bitrate: %v", a.BitrateKbps)
	}
	if a.Score == nil || a.Score.Min != 70 || len(a.Scores) != 240 {
		t.Errorf("scores: %+v", a.Score)
	}
	if a.Fallback == nil || a.Fallback.Src != "a-fallback.mp4" {
		t.Errorf("fallback: %+v", a.Fallback)
	}
	if m.Reference.Src != "reference.mp4" || m.Reference.Codec != "H.264 High" || len(m.Reference.Scores) != 0 {
		t.Errorf("reference: %+v", m.Reference)
	}

	// every pair gets its own jump points, so the viewer can swap encodes
	for _, key := range []string{"a|b", "a|c", "b|c"} {
		if len(m.Moments[key]) == 0 {
			t.Errorf("no moments for pair %s", key)
		}
	}
	if len(m.Moments) != 3 {
		t.Errorf("want exactly the three pairs, got %d keys", len(m.Moments))
	}

	want := map[string]bool{"a.mp4": true, "a-fallback.mp4": true, "b.mp4": true, "c.mp4": true, "reference.mp4": true}
	for _, f := range files {
		if !want[f.Src] {
			t.Errorf("unexpected file %s", f.Src)
		}
		delete(want, f.Src)
	}
	if len(want) != 0 {
		t.Errorf("files missing: %v", want)
	}

	if m.Notes == nil || len(m.Notes.Commands) != 2 || m.Notes.Commands[0].Encode != "a" {
		t.Fatalf("notes: %+v", m.Notes)
	}
	if got := m.Notes.Commands[0].Text; got != "ffmpeg -i in.mov -crf 25 a.mp4" {
		t.Errorf("command was not scrubbed: %q", got)
	}
	if m.Metric.Model != "vmaf_v1.json" || m.Subtitle != "cut from in.mov" || a.Detail != "built in enc" {
		t.Errorf("scrub missed a field: model %q, subtitle %q, detail %q", m.Metric.Model, m.Subtitle, a.Detail)
	}
}

func TestBuildSingleEncode(t *testing.T) {
	opts := fixture(t)
	opts.Encodes[1], opts.Encodes[2] = Input{}, Input{}
	m, _, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Encodes) != 1 || m.Compare.B != "" {
		t.Errorf("got %d encodes, compare %+v", len(m.Encodes), m.Compare)
	}
	if len(m.Moments["a"]) == 0 {
		t.Errorf("a lone encode should still list its dips: %+v", m.Moments)
	}
}

func TestBuildErrors(t *testing.T) {
	if _, _, err := Build(context.Background(), &Options{}); err == nil {
		t.Error("want an error with no encodes")
	}
	opts := fixture(t)
	opts.Encodes[1].Path = filepath.Join(t.TempDir(), "missing.mp4")
	if _, _, err := Build(context.Background(), opts); err == nil {
		t.Error("want an error for a missing video")
	}

	// a failed probe is survivable: the flag supplies the rate
	opts = fixture(t)
	opts.FPS = 30
	opts.Probe = func(context.Context, string) (*MediaInfo, error) { return nil, errors.New("no ffprobe") }
	m, _, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if m.FPS != 30 || m.Encodes[0].Mime != "video/mp4" || m.Frames != 240 {
		t.Errorf("without a probe: fps %v, mime %q, frames %d", m.FPS, m.Encodes[0].Mime, m.Frames)
	}
}

func TestLoadNotesPlainText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("First paragraph,\nwrapped.\n\n\nSecond one.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := LoadNotes(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Body) != 2 || n.Body[0] != "First paragraph, wrapped." || n.Body[1] != "Second one." {
		t.Errorf("got %q", n.Body)
	}
}

func TestExport(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site", "compare")
	res, err := Export(context.Background(), fixture(t), out)
	if err != nil {
		t.Fatal(err)
	}
	m := res.Manifest

	for _, name := range []string{
		"index.html", "css/player.css", "js/player.js", "manifest.json", ".nojekyll",
		"media/a.mp4", "media/a-fallback.mp4", "media/b.mp4", "media/c.mp4", "media/reference.mp4",
		"media/a/index.m3u8", "media/a/init.mp4", "media/a/seg-00000.m4s", "media/a/seg-00001.m4s",
		"media/reference/index.m3u8",
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}

	body, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var back Manifest
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("manifest.json doesn't parse: %v", err)
	}
	if back.Title != m.Title || len(back.Encodes) != 3 || len(back.Encodes[0].Scores) != 240 {
		t.Errorf("manifest round trip lost data: %+v", back)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "media", "b.mp4")); len(got) != 3030 {
		t.Errorf("media/b.mp4 is %d bytes, want 3030", len(got))
	}

	// the manifest names every segment, relative to mediaBase, with its own clock offset
	seg := back.Encodes[0].Segments
	if seg == nil || seg.Playlist != "a/index.m3u8" || seg.Init != "a/init.mp4" || len(seg.List) != 2 {
		t.Fatalf("segments: %+v", seg)
	}
	if seg.Start != 0.125 || seg.List[1].Src != "a/seg-00001.m4s" || seg.List[1].Start != 2 || seg.List[1].Duration != 1 {
		t.Errorf("segment timing: start %v, %+v", seg.Start, seg.List[1])
	}
	if seg.List[0].Bytes == 0 {
		t.Error("segment size missing")
	}
	// a fallback is a different picture, so it is never cut up as the encode
	if _, err := os.Stat(filepath.Join(out, "media", "a-fallback")); err == nil {
		t.Error("the fallback was segmented")
	}

	total, err := DirSize(out)
	if err != nil || total != res.Bytes || total < 3000+3030+2990 {
		t.Errorf("total size: %d, DirSize %d, %v", res.Bytes, total, err)
	}
}

func TestExportOptions(t *testing.T) {
	opts := fixture(t)
	opts.NoProgressive = true
	opts.Ref = Input{Label: "Master"}
	opts.RefURL = "https://media.example.com/tears/source.mp4"
	opts.MediaBase = "https://media.example.com/tears"
	opts.Variants = []Variant{{Label: "1080p"}, {Label: "4K", Href: "../tears-4k/"}}
	out := t.TempDir()
	res, err := Export(context.Background(), opts, out)
	if err != nil {
		t.Fatal(err)
	}
	m := res.Manifest
	if m.MediaBase != "https://media.example.com/tears/" {
		t.Errorf("mediaBase: %q", m.MediaBase)
	}
	if len(m.Variants) != 2 || m.Variants[1].Href != "../tears-4k/" {
		t.Errorf("variants: %+v", m.Variants)
	}
	// only the segments are hosted
	if m.Encodes[0].Src != "" || m.Encodes[0].Segments == nil {
		t.Errorf("encode a: src %q, segments %v", m.Encodes[0].Src, m.Encodes[0].Segments)
	}
	if _, err := os.Stat(filepath.Join(out, "media", "a.mp4")); err == nil {
		t.Error("media/a.mp4 was written despite NoProgressive")
	}
	// an outside reference is named, not copied
	ref := m.Reference
	if ref == nil || ref.Src != opts.RefURL || ref.Label != "Master" || ref.Segments != nil || ref.Bytes != 0 {
		t.Errorf("reference: %+v", ref)
	}
	if _, err := os.Stat(filepath.Join(out, "media", "reference")); err == nil {
		t.Error("something was written for an outside reference")
	}

	opts = fixture(t)
	opts.NoSegments = true
	res, err = Export(context.Background(), opts, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.Encodes[0].Segments != nil || res.Manifest.Encodes[0].Src != "a.mp4" {
		t.Errorf("NoSegments: %+v", res.Manifest.Encodes[0])
	}
}

func TestWriteIndex(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"tears-1080p", "tears-4k"} {
		opts := fixture(t)
		opts.Title = "Tears <of> Steel"
		opts.Variants = []Variant{{Label: name[6:]}}
		if _, err := Export(context.Background(), opts, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	n, total, err := WriteIndex(root, "Comparisons")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{`href="tears-1080p/"`, `href="tears-4k/"`, "Tears &lt;of&gt; Steel", "1080p", "theirs, tuned"} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	if n != 2 || total <= int64(len(body)) {
		t.Errorf("listed %d, total %d", n, total)
	}
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Error("index.html loads from another site")
	}
	if _, _, err := WriteIndex(t.TempDir(), "x"); err == nil {
		t.Error("want an error for a folder with no exports")
	}
}

// The export is published as is, so nothing in it may name a path on the
// machine that made it, and nothing may load from another site.
func TestExportIsSelfContainedAndPrivate(t *testing.T) {
	out := t.TempDir()
	if _, err := Export(context.Background(), fixture(t), out); err != nil {
		t.Fatal(err)
	}
	remote := []string{`src="http`, `href="http`, `src="//`, `href="//`, "url(http", "@import", "cdn."}
	err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, path)
		for _, private := range []string{"/Users/", "/Volumes/"} {
			if i := bytes.Index(body, []byte(private)); i >= 0 {
				t.Errorf("%s contains %q: ...%s...", rel, private, body[max(0, i-30):min(len(body), i+50)])
			}
		}
		switch filepath.Ext(path) {
		case ".html", ".css", ".js":
			for _, r := range remote {
				if bytes.Contains(body, []byte(r)) {
					t.Errorf("%s loads from another site (%q)", rel, r)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExportRefusesLeftoverPath(t *testing.T) {
	// a path the scrub rule doesn't understand must stop the export, not ship
	opts := fixture(t)
	opts.Title = "see file:///Users/someone/clip"
	if _, err := Export(context.Background(), opts, t.TempDir()); err == nil || !strings.Contains(err.Error(), "local path") {
		t.Errorf("want a local-path error, got %v", err)
	}
}

func TestHandler(t *testing.T) {
	opts := fixture(t)
	m, files, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(m, files)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	get := func(path string, header ...string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(header) == 2 {
			req.Header.Set(header[0], header[1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, body
	}

	res, body := get("/")
	if res.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("js/player.js")) {
		t.Errorf("index: status %d", res.StatusCode)
	}
	if res, _ = get("/js/player.js"); res.StatusCode != http.StatusOK {
		t.Errorf("player.js: status %d", res.StatusCode)
	}

	res, body = get("/manifest.json")
	var back Manifest
	if err := json.Unmarshal(body, &back); err != nil || back.Title != "Test title" {
		t.Errorf("manifest: status %d, %v", res.StatusCode, err)
	}

	// seeking depends on range requests
	res, body = get("/media/b.mp4", "Range", "bytes=10-19")
	if res.StatusCode != http.StatusPartialContent || string(body) != "bbbbbbbbbb" {
		t.Errorf("range: status %d, body %q", res.StatusCode, body)
	}
	if res, _ = get("/media/nope.mp4"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown media: status %d", res.StatusCode)
	}
}
