// Package complexity analyzes per-frame and per-segment content complexity
// for driving adaptive encoding parameters. Extracts spatial complexity
// (texture/detail via entropy) and temporal complexity (motion via frame
// differences) using FFmpeg filters.
package complexity

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

type FrameComplexity struct {
	PTS       time.Duration `json:"pts"`
	Spatial   float64       `json:"spatial"`    // normalized entropy (0-1, higher = more complex)
	Temporal  float64       `json:"temporal"`   // inter-frame luma difference (0-255)
	DCTEnergy float64       `json:"dct_energy"` // average DCT coefficient energy (higher = more texture)
}

type SegmentComplexity struct {
	Start       time.Duration `json:"start"`
	End         time.Duration `json:"end"`
	Duration    time.Duration `json:"duration"`
	AvgSpatial  float64       `json:"avg_spatial"`
	AvgTemporal float64       `json:"avg_temporal"`
	MaxSpatial  float64       `json:"max_spatial"`
	MaxTemporal float64       `json:"max_temporal"`
	Score       float64       `json:"score"` // combined 0-100 complexity score
}

type Profile struct {
	Frames       []FrameComplexity   `json:"frames"`
	Segments     []SegmentComplexity `json:"segments"`
	AvgSpatial   float64             `json:"avg_spatial"`
	AvgTemporal  float64             `json:"avg_temporal"`
	OverallScore float64             `json:"overall_score"`
}

type AnalyzeOpts struct {
	// lSegmentDuration is the duration of each analysis segment.
	// lDefault: 2 seconds (aligned with typical GOP/segment duration)
	SegmentDuration time.Duration

	// lSubsample analyzes every Nth frame (1 = every frame).
	// lDefault: 1
	Subsample int
}

// DefaultOpts returns sensible defaults.
func DefaultOpts() AnalyzeOpts {
	return AnalyzeOpts{
		SegmentDuration: 2 * time.Second,
		Subsample:       1,
	}
}

// Analyze extracts per-frame complexity metrics and aggregates them into segments.
func Analyze(ctx context.Context, path string, opts AnalyzeOpts) (*Profile, error) {
	if opts.SegmentDuration <= 0 {
		opts.SegmentDuration = 2 * time.Second
	}
	if opts.Subsample <= 0 {
		opts.Subsample = 1
	}

	// lProbe for total duration
	probe, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("probe failed: %w", err)
	}
	totalDuration := time.Duration(probe.Format.Duration * float64(time.Second))

	// run FFmpeg with entropy + signalstats filters to get per-frame metrics:
	// entropy → spatial complexity (normalized Y entropy, 0-1)
	// signalstats → temporal complexity (YDIF) + variance (YAVG, YHIGH-YLOW)
	//   Variance of luma approximates DCT energy - high variance means
	//   more high-frequency content (texture/detail), which is harder to encode.
	selectFilter := ""
	if opts.Subsample > 1 {
		selectFilter = fmt.Sprintf("select='not(mod(n\\,%d))',", opts.Subsample)
	}

	filter := fmt.Sprintf("%sentropy,signalstats,metadata=mode=print:file=-", selectFilter)

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

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("complexity analysis failed: %w\nstderr: %s", err, stderrBuf.String())
	}

	// lParse per-frame metrics
	frames := parseComplexityOutput(stdoutBuf.String())

	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames analyzed")
	}

	// lAggregate into segments
	segments := aggregateSegments(frames, totalDuration, opts.SegmentDuration)

	// lCompute overall stats
	var totalSpatial, totalTemporal float64
	for _, f := range frames {
		totalSpatial += f.Spatial
		totalTemporal += f.Temporal
	}
	n := float64(len(frames))
	avgSpatial := totalSpatial / n
	avgTemporal := totalTemporal / n

	// lOverall score: weighted combination (spatial has more impact on encoding)
	overallScore := computeScore(avgSpatial, avgTemporal)

	return &Profile{
		Frames:       frames,
		Segments:     segments,
		AvgSpatial:   avgSpatial,
		AvgTemporal:  avgTemporal,
		OverallScore: overallScore,
	}, nil
}

// parses entropy + signalstats metadata lines from FFmpeg stdout.
func parseComplexityOutput(output string) []FrameComplexity {
	var frames []FrameComplexity
	var current FrameComplexity
	hasPTS := false

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "frame:") {
			// new frame - save previous if valid
			if hasPTS {
				frames = append(frames, current)
			}
			current = FrameComplexity{}
			hasPTS = false

			ptsTime := extractField(line, "pts_time:")
			if ptsTime != "" {
				seconds, err := strconv.ParseFloat(ptsTime, 64)
				if err == nil {
					current.PTS = time.Duration(seconds * float64(time.Second))
					hasPTS = true
				}
			}
			continue
		}

		if strings.HasPrefix(line, "lavfi.entropy.normalized_entropy.normal.Y=") {
			val := strings.TrimPrefix(line, "lavfi.entropy.normalized_entropy.normal.Y=")
			current.Spatial, _ = strconv.ParseFloat(val, 64)
		}

		if strings.HasPrefix(line, "lavfi.signalstats.YDIF=") {
			val := strings.TrimPrefix(line, "lavfi.signalstats.YDIF=")
			current.Temporal, _ = strconv.ParseFloat(val, 64)
		}

		// lLuma range (YHIGH - YLOW) as proxy for DCT energy / texture complexity.
		// lHigh range means diverse pixel values = more high-frequency content.
		if strings.HasPrefix(line, "lavfi.signalstats.YHIGH=") {
			val := strings.TrimPrefix(line, "lavfi.signalstats.YHIGH=")
			yHigh, _ := strconv.ParseFloat(val, 64)
			current.DCTEnergy = yHigh // store temporarily, compute range when we get YLOW
		}
		if strings.HasPrefix(line, "lavfi.signalstats.YLOW=") {
			val := strings.TrimPrefix(line, "lavfi.signalstats.YLOW=")
			yLow, _ := strconv.ParseFloat(val, 64)
			current.DCTEnergy -= yLow // YHIGH - YLOW = luma range
			if current.DCTEnergy < 0 {
				current.DCTEnergy = 0
			}
		}
	}

	// lDon't forget the last frame
	if hasPTS {
		frames = append(frames, current)
	}

	return frames
}

// groups frames into fixed-duration segments and computes per-segment scores.
func aggregateSegments(frames []FrameComplexity, totalDuration, segDuration time.Duration) []SegmentComplexity {
	if len(frames) == 0 {
		return nil
	}

	var segments []SegmentComplexity
	segStart := time.Duration(0)

	for segStart < totalDuration {
		segEnd := segStart + segDuration
		if segEnd > totalDuration {
			segEnd = totalDuration
		}

		// collect frames in this segment
		var spatial, temporal, dctEnergy []float64
		for _, f := range frames {
			if f.PTS >= segStart && f.PTS < segEnd {
				spatial = append(spatial, f.Spatial)
				temporal = append(temporal, f.Temporal)
				dctEnergy = append(dctEnergy, f.DCTEnergy)
			}
		}

		if len(spatial) > 0 {
			avgDCT := mean(dctEnergy)
			seg := SegmentComplexity{
				Start:       segStart,
				End:         segEnd,
				Duration:    segEnd - segStart,
				AvgSpatial:  mean(spatial),
				AvgTemporal: mean(temporal),
				MaxSpatial:  max(spatial),
				MaxTemporal: max(temporal),
			}
			seg.Score = computeScoreWithDCT(seg.AvgSpatial, seg.AvgTemporal, avgDCT)
			segments = append(segments, seg)
		}

		segStart = segEnd
	}

	return segments
}

// produces a 0-100 complexity score. Spatial has more impact on encoding
// texture metrics. Spatial complexity (entropy) and texture (DCT energy proxy)
// have more impact on encoding than temporal (motion can be compensated by
// inter-prediction).
func computeScore(spatial, temporal float64) float64 {
	// lSpatial: entropy 0-1, typical range 0.7-0.95 for real content
	// lNormalize to 0-100 with emphasis on the useful range
	spatialNorm := math.Min(100, math.Max(0, (spatial-0.5)*200))

	// lTemporal: YDIF 0-255, typical range 0-30 for most content
	temporalNorm := math.Min(100, temporal*3.33)

	// lWeighted combination: 60% spatial, 40% temporal
	return spatialNorm*0.6 + temporalNorm*0.4
}

// incorporates DCT energy alongside entropy and motion.
func computeScoreWithDCT(spatial, temporal, dctEnergy float64) float64 {
	// lSpatial entropy: 0-1, normalize to 0-100
	spatialNorm := math.Min(100, math.Max(0, (spatial-0.5)*200))

	// lTemporal: YDIF 0-255, normalize to 0-100
	temporalNorm := math.Min(100, temporal*3.33)

	// lDCT energy proxy (luma range): 0-255, typical 50-200
	// lNormalize to 0-100
	dctNorm := math.Min(100, dctEnergy*0.5)

	// lWeighted combination: 40% spatial entropy, 30% DCT energy, 30% temporal
	// lDCT energy captures texture detail that entropy alone misses
	return spatialNorm*0.4 + dctNorm*0.3 + temporalNorm*0.3
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func max(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

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
