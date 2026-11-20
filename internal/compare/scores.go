package compare

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
)

// LoadScores reads one value per frame from a libvmaf JSON log
// (frames[].metrics.<key>) or from `veo quality measure --per-frame -o`
// output (frames[].vmaf). Frames a subsampled log skipped are interpolated.
func LoadScores(path, key string) (Scores, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read quality log: %w", err)
	}
	scores, err := ParseScores(data, key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return scores, nil
}

func ParseScores(data []byte, key string) (Scores, error) {
	var log struct {
		Frames []struct {
			FrameNum int                `json:"frameNum"`
			Metrics  map[string]float64 `json:"metrics"`
			VMAF     *float64           `json:"vmaf"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("failed to parse quality log: %w", err)
	}
	if len(log.Frames) == 0 {
		return nil, fmt.Errorf("quality log has no frames; measure with per-frame output")
	}

	if log.Frames[0].Metrics != nil {
		resolved, err := resolveKey(log.Frames[0].Metrics, key)
		if err != nil {
			return nil, err
		}
		key = resolved
	}

	last := 0
	for _, f := range log.Frames {
		if f.FrameNum < 0 {
			return nil, fmt.Errorf("quality log has a negative frame number %d", f.FrameNum)
		}
		last = max(last, f.FrameNum)
	}
	out := make(Scores, last+1)
	have := make([]bool, last+1)
	for _, f := range log.Frames {
		switch {
		case f.Metrics != nil:
			v, ok := f.Metrics[key]
			if !ok {
				return nil, fmt.Errorf("frame %d has no %q metric", f.FrameNum, key)
			}
			out[f.FrameNum] = v
		case f.VMAF != nil:
			out[f.FrameNum] = *f.VMAF
		default:
			return nil, fmt.Errorf("frame %d has no metrics", f.FrameNum)
		}
		have[f.FrameNum] = true
	}
	fillGaps(out, have)
	return out, nil
}

// resolveKey lets "vmaf" match a log whose only VMAF model has its own name,
// as libvmaf writes when a model is loaded with name=.
func resolveKey(metrics map[string]float64, key string) (string, error) {
	if _, ok := metrics[key]; ok {
		return key, nil
	}
	var names, like []string
	for name := range metrics {
		names = append(names, name)
		if strings.HasPrefix(name, key) {
			like = append(like, name)
		}
	}
	if len(like) == 1 {
		return like[0], nil
	}
	sort.Strings(names)
	return "", fmt.Errorf("quality log has no %q metric; it has %s", key, strings.Join(names, ", "))
}

func fillGaps(v []float64, have []bool) {
	prev := -1
	for i := range v {
		if !have[i] {
			continue
		}
		for j := prev + 1; j < i; j++ {
			if prev < 0 {
				v[j] = v[i]
			} else {
				t := float64(j-prev) / float64(i-prev)
				v[j] = v[prev] + t*(v[i]-v[prev])
			}
		}
		prev = i
	}
}

// Summarize returns nil for an empty series.
func Summarize(v []float64) *Stats {
	if len(v) == 0 {
		return nil
	}
	sorted := slices.Clone(v)
	sort.Float64s(sorted)
	var sum float64
	for _, x := range v {
		sum += x
	}
	return &Stats{
		Mean: round(sum/float64(len(v)), 3),
		Min:  round(sorted[0], 3),
		Max:  round(sorted[len(sorted)-1], 3),
		P5:   round(sorted[int(0.05*float64(len(sorted)-1))], 3),
	}
}

const (
	// a low point counts as a dip once it sits this far under the stream's mean
	minDip = 1.0
	// the two streams count as apart once they differ by this much
	minLead = 0.5
)

// FindMoments picks the places a viewer should look at: each encode's
// lowest stretches, and the stretches where one is furthest ahead of the
// other. b may be empty. The result is in frame order.
func FindMoments(a, b []float64, fps float64, perKind int) []Moment {
	out := []Moment{}
	if len(a) == 0 || fps <= 0 || perKind <= 0 {
		return out
	}
	if len(b) > 0 {
		n := min(len(a), len(b))
		a, b = a[:n], b[:n]
	}
	n := len(a)
	// half a second of smoothing ranks stretches a viewer can see over one-frame blips
	window := max(1, int(math.Round(fps/2)))
	// picks stay two seconds apart, or further on a long title
	sep := max(int(math.Round(2*fps)), n/(perKind*6))

	emit := func(kind, side string, frames []int) {
		for rank, f := range frames {
			out = append(out, Moment{Kind: kind, Side: side, Rank: rank + 1, Frame: f, Time: round(float64(f)/fps, 3)})
		}
	}

	dips := func(v []float64) []int {
		sm := smooth(v, window)
		var mean float64
		for _, x := range v {
			mean += x
		}
		mean /= float64(len(v))
		neg := make([]float64, len(sm))
		for i, x := range sm {
			neg[i] = mean - x
		}
		// the single worst stretch is always listed, however shallow
		picks := peaks(neg, perKind, sep, minDip, true)
		return refine(picks, window, len(v), func(i int) float64 { return -v[i] })
	}
	emit(KindDip, "a", dips(a))

	if len(b) > 0 {
		emit(KindDip, "b", dips(b))

		delta := make([]float64, n)
		for i := range delta {
			delta[i] = a[i] - b[i]
		}
		sm := smooth(delta, window)
		emit(KindLead, "a", refine(peaks(sm, perKind, sep, minLead, false), window, n,
			func(i int) float64 { return delta[i] }))
		for i := range sm {
			sm[i] = -sm[i]
		}
		emit(KindLead, "b", refine(peaks(sm, perKind, sep, minLead, false), window, n,
			func(i int) float64 { return -delta[i] }))
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Frame < out[j].Frame })
	return out
}

// smooth is a centered moving average, shortened at the ends.
func smooth(v []float64, window int) []float64 {
	prefix := make([]float64, len(v)+1)
	for i, x := range v {
		prefix[i+1] = prefix[i] + x
	}
	out := make([]float64, len(v))
	half := window / 2
	for i := range v {
		lo, hi := max(0, i-half), min(len(v), i+half+1)
		out[i] = (prefix[hi] - prefix[lo]) / float64(hi-lo)
	}
	return out
}

// peaks returns up to n indexes of v's highest values, strongest first, no two
// closer than sep, each at least floor. With always, the highest is kept even
// under floor.
func peaks(v []float64, n, sep int, floor float64, always bool) []int {
	taken := make([]bool, len(v))
	var out []int
	for len(out) < n {
		best := -1
		for i, x := range v {
			if !taken[i] && (best < 0 || x > v[best]) {
				best = i
			}
		}
		if best < 0 || (v[best] < floor && (!always || len(out) > 0)) {
			break
		}
		out = append(out, best)
		for i := max(0, best-sep); i <= min(len(v)-1, best+sep); i++ {
			taken[i] = true
		}
	}
	return out
}

// refine moves each smoothed pick to the strongest raw frame beside it, so the
// values printed for a moment are the ones on screen after the jump.
func refine(picks []int, window, n int, strength func(i int) float64) []int {
	out := make([]int, len(picks))
	for k, p := range picks {
		best := p
		for i := max(0, p-window/2); i <= min(n-1, p+window/2); i++ {
			if strength(i) > strength(best) {
				best = i
			}
		}
		out[k] = best
	}
	return out
}
