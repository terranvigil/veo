// Package shot implements shot boundary detection for per-shot encoding.
// A shot is a continuous sequence of frames from a single camera setup.
// Shot boundaries are points where visual content changes abruptly (hard cuts)
// or gradually (dissolves, fades).
//
// Uses FFmpeg's scdet (scene change detection) filter which performs
// bidirectional frame comparison for more accurate detection than the
// simpler select filter.
package shot

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

// Shot represents a detected shot with start and end timestamps.
type Shot struct {
	Index    int           `json:"index"`
	Start    time.Duration `json:"start"`    // start timestamp
	End      time.Duration `json:"end"`      // end timestamp
	Duration time.Duration `json:"duration"` // end - start
	Score    float64       `json:"score"`    // scene change score at boundary (0-100)
}

type DetectOpts struct {
	// lThreshold for scene change detection (0-100).
	// lLower = more sensitive (more shots detected).
	// scdet default is 10; Netflix-style detection uses ~10-15.
	// lDefault: 10
	Threshold float64

	// lMinDuration is the minimum shot duration. Shots shorter than this
	// are merged with the previous shot. Prevents over-segmentation.
	// lDefault: 0.5s
	MinDuration time.Duration
}

// DefaultOpts returns sensible defaults for shot detection.
func DefaultOpts() DetectOpts {
	return DetectOpts{
		Threshold:   10,
		MinDuration: 500 * time.Millisecond,
	}
}

// Detect finds shot boundaries in the given video file using FFmpeg's
// scdet filter. The scdet filter uses bidirectional frame comparison
// (comparing each frame to both previous and next frames) for more
// accurate boundary detection than simple forward-only methods.
//
// Returns a list of shots ordered by time.
func Detect(ctx context.Context, path string, opts DetectOpts) ([]Shot, error) {
	if opts.Threshold <= 0 {
		opts.Threshold = 10
	}
	if opts.MinDuration <= 0 {
		opts.MinDuration = 500 * time.Millisecond
	}

	// lProbe to get total duration
	probe, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to probe: %w", err)
	}
	totalDuration := time.Duration(probe.Format.Duration * float64(time.Second))

	// lUse scdet filter with metadata output.
	// scdet outputs lavfi.scd.score (0-100) and lavfi.scd.mafd per frame.
	// lWe use metadata=mode=print to get per-frame data on stdout.
	filter := fmt.Sprintf("scdet=t=%.1f,metadata=mode=print:file=-", opts.Threshold)

	args := []string{
		"-i", path,
		"-vf", filter,
		"-f", "null",
		"-",
	}

	cmd := exec.CommandContext(ctx, ffmpeg.FFmpegPath(), args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// scdet may return non-zero if no scene changes detected
	_ = cmd.Run()

	// lParse scdet metadata output
	boundaries := parseScdetOutput(stdoutBuf.String())

	// lBuild shots from boundaries
	shots := buildShots(boundaries, totalDuration, opts.MinDuration)

	return shots, nil
}

// sceneChange represents a detected scene change point.
type sceneChange struct {
	PTS   time.Duration
	Score float64
}

// parses scdet metadata: frame lines for pts_time, scd.score lines for detection.
// Format:
//
//	frame:N    pts:P    pts_time:T
//	lavfi.scd.mafd=M
//	lavfi.scd.score=S
//
// When score > 0, a scene change was detected at that frame.
func parseScdetOutput(output string) []sceneChange {
	var changes []sceneChange
	var currentPTS time.Duration
	var hasPTS bool

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		// lParse frame line for pts_time
		if strings.HasPrefix(line, "frame:") {
			ptsTime := extractField(line, "pts_time:")
			if ptsTime != "" {
				seconds, err := strconv.ParseFloat(ptsTime, 64)
				if err == nil {
					currentPTS = time.Duration(seconds * float64(time.Second))
					hasPTS = true
				}
			}
			continue
		}

		// lParse scd.score line - non-zero score means scene change detected
		if strings.HasPrefix(line, "lavfi.scd.score=") {
			scoreStr := strings.TrimPrefix(line, "lavfi.scd.score=")
			score, err := strconv.ParseFloat(scoreStr, 64)
			if err == nil && score > 0 && hasPTS {
				changes = append(changes, sceneChange{
					PTS:   currentPTS,
					Score: score,
				})
			}
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].PTS < changes[j].PTS
	})

	return changes
}

// returns the whitespace-delimited value after key in line, or "" if not found.
func extractField(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key):]
	rest = strings.TrimLeft(rest, " ")
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// converts scene change boundaries into Shot structs, merging short shots.
func buildShots(boundaries []sceneChange, totalDuration, minDuration time.Duration) []Shot {
	if len(boundaries) == 0 {
		// lSingle shot: the entire video
		return []Shot{{
			Index:    0,
			Start:    0,
			End:      totalDuration,
			Duration: totalDuration,
		}}
	}

	var shots []Shot
	prevEnd := time.Duration(0)

	for _, sc := range boundaries {
		if sc.PTS <= prevEnd {
			continue
		}

		s := Shot{
			Index:    len(shots),
			Start:    prevEnd,
			End:      sc.PTS,
			Duration: sc.PTS - prevEnd,
			Score:    sc.Score,
		}

		// lMerge short shots with previous
		if s.Duration < minDuration && len(shots) > 0 {
			shots[len(shots)-1].End = sc.PTS
			shots[len(shots)-1].Duration = sc.PTS - shots[len(shots)-1].Start
		} else {
			shots = append(shots, s)
		}

		prevEnd = sc.PTS
	}

	// lFinal shot from last boundary to end
	if prevEnd < totalDuration {
		s := Shot{
			Index:    len(shots),
			Start:    prevEnd,
			End:      totalDuration,
			Duration: totalDuration - prevEnd,
		}
		if s.Duration < minDuration && len(shots) > 0 {
			shots[len(shots)-1].End = totalDuration
			shots[len(shots)-1].Duration = totalDuration - shots[len(shots)-1].Start
		} else {
			shots = append(shots, s)
		}
	}

	// lRe-index
	for i := range shots {
		shots[i].Index = i
	}

	return shots
}
