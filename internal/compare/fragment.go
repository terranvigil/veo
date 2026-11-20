package compare

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const boxMoov = "moov"

// box is one ISO BMFF box read whole.
type box struct {
	kind string
	data []byte // header included
	body []byte
}

func readBox(r io.Reader) (*box, error) {
	var head [16]byte
	if _, err := io.ReadFull(r, head[:8]); err != nil {
		return nil, err
	}
	size := uint64(binary.BigEndian.Uint32(head[:4]))
	used := uint64(8)
	if size == 1 {
		if _, err := io.ReadFull(r, head[8:16]); err != nil {
			return nil, err
		}
		size = binary.BigEndian.Uint64(head[8:16])
		used = 16
	}
	if size < used || size > 1<<34 {
		return nil, fmt.Errorf("bad %q box size %d", head[4:8], size)
	}
	data := make([]byte, size)
	copy(data, head[:used])
	if _, err := io.ReadFull(r, data[used:]); err != nil {
		return nil, err
	}
	return &box{kind: string(head[4:8]), data: data, body: data[used:]}, nil
}

// child returns the body of the first box of each kind along path, or nil.
func child(body []byte, path ...string) []byte {
	for _, kind := range path {
		var found []byte
		for len(body) >= 8 {
			size := int(binary.BigEndian.Uint32(body[:4]))
			if size < 8 || size > len(body) {
				break
			}
			if string(body[4:8]) == kind {
				found = body[8:size]
				break
			}
			body = body[size:]
		}
		if found == nil {
			return nil
		}
		body = found
	}
	return body
}

// timescale reads the video track's units per second from a moov body.
func timescale(moov []byte) (uint32, error) {
	mdhd := child(moov, "trak", "mdia", "mdhd")
	if len(mdhd) < 4 {
		return 0, errors.New("no mdhd box in the init segment")
	}
	at := 12 // version 0: flags, creation, modification
	if mdhd[0] == 1 {
		at = 20
	}
	if len(mdhd) < at+4 {
		return 0, errors.New("short mdhd box")
	}
	if ts := binary.BigEndian.Uint32(mdhd[at:]); ts > 0 {
		return ts, nil
	}
	return 0, errors.New("mdhd has no timescale")
}

// fragmentTimes returns one moof's decode duration and the earliest
// presentation time of its samples, both in track units.
func fragmentTimes(moof []byte) (duration, firstPTS int64, err error) {
	traf := child(moof, "traf")
	tfhd, tfdt, trun := child(traf, "tfhd"), child(traf, "tfdt"), child(traf, "trun")
	if len(tfhd) < 8 || len(tfdt) < 8 || len(trun) < 8 {
		return 0, 0, errors.New("fragment is missing tfhd, tfdt or trun")
	}

	// tfhd: optional fields in flag order, up to the default sample duration
	flags := binary.BigEndian.Uint32(tfhd[:4]) & 0xffffff
	at := 8
	if flags&0x1 != 0 {
		at += 8
	}
	if flags&0x2 != 0 {
		at += 4
	}
	var defaultDuration int64
	if flags&0x8 != 0 {
		if len(tfhd) < at+4 {
			return 0, 0, errors.New("short tfhd box")
		}
		defaultDuration = int64(binary.BigEndian.Uint32(tfhd[at:]))
	}

	var base int64
	if tfdt[0] == 1 && len(tfdt) >= 12 {
		base = int64(binary.BigEndian.Uint64(tfdt[4:]))
	} else {
		base = int64(binary.BigEndian.Uint32(tfdt[4:]))
	}

	version := trun[0]
	flags = binary.BigEndian.Uint32(trun[:4]) & 0xffffff
	count := int(binary.BigEndian.Uint32(trun[4:]))
	at = 8
	if flags&0x1 != 0 {
		at += 4
	}
	if flags&0x4 != 0 {
		at += 4
	}
	dts := base
	firstPTS = math.MaxInt64
	for i := 0; i < count; i++ {
		sample := defaultDuration
		var offset int64
		for _, field := range []uint32{0x100, 0x200, 0x400, 0x800} {
			if flags&field == 0 {
				continue
			}
			if len(trun) < at+4 {
				return 0, 0, errors.New("short trun box")
			}
			v := binary.BigEndian.Uint32(trun[at:])
			at += 4
			switch field {
			case 0x100:
				sample = int64(v)
			case 0x800:
				offset = int64(v)
				if version != 0 {
					offset = int64(int32(v))
				}
			}
		}
		firstPTS = min(firstPTS, dts+offset)
		dts += sample
	}
	if count == 0 {
		firstPTS = base
	}
	return dts - base, firstPTS, nil
}

// SplitFragments cuts a fragmented MP4 into dir as init.mp4, one seg-NNNNN.m4s
// per movie fragment and an HLS playlist over them. It returns the first
// frame's presentation time in seconds. No sample is touched: the output is
// the input's boxes, regrouped.
func SplitFragments(r io.Reader, dir string) (start float64, err error) {
	var init []byte
	var scale uint32
	var durations []float64
	var seg []byte
	flush := func() error {
		if seg == nil {
			return nil
		}
		name := fmt.Sprintf("seg-%05d.m4s", len(durations)-1)
		err := os.WriteFile(filepath.Join(dir, name), seg, 0o644)
		seg = nil
		return err
	}

	for {
		b, err := readBox(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("failed to read fragmented MP4: %w", err)
		}
		switch b.kind {
		case "ftyp", boxMoov:
			init = append(init, b.data...)
			if b.kind == boxMoov {
				if scale, err = timescale(b.body); err != nil {
					return 0, err
				}
			}
		case "moof":
			if scale == 0 {
				return 0, errors.New("fragment before the init segment")
			}
			if err := flush(); err != nil {
				return 0, err
			}
			duration, first, err := fragmentTimes(b.body)
			if err != nil {
				return 0, err
			}
			if len(durations) == 0 {
				start = float64(first) / float64(scale)
			}
			durations = append(durations, float64(duration)/float64(scale))
			seg = b.data
		case "mdat":
			if seg == nil {
				return 0, errors.New("not a fragmented MP4: media data outside a fragment")
			}
			seg = append(seg, b.data...)
		case "styp", "sidx":
			// a new segment's preamble; default_base_moof fragments stand without it
		case "mfra":
			// the random access index points into the whole file, which no longer exists
		}
	}
	if err := flush(); err != nil {
		return 0, err
	}
	if init == nil || len(durations) == 0 {
		return 0, errors.New("not a fragmented MP4: no fragments found")
	}
	if err := os.WriteFile(filepath.Join(dir, initName), init, 0o644); err != nil {
		return 0, err
	}

	var longest float64
	for _, d := range durations {
		longest = max(longest, d)
	}
	var pl strings.Builder
	fmt.Fprintf(&pl, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n", int(math.Ceil(longest)))
	fmt.Fprintf(&pl, "#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=%q\n", initName)
	for i, d := range durations {
		fmt.Fprintf(&pl, "#EXTINF:%.6f,\nseg-%05d.m4s\n", d, i)
	}
	pl.WriteString("#EXT-X-ENDLIST\n")
	return start, os.WriteFile(filepath.Join(dir, playlistName), []byte(pl.String()), 0o644)
}
