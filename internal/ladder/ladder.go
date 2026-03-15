// Package ladder selects an optimal bitrate ladder from convex hull points.
// Given a set of Pareto-optimal operating points, it picks N rungs that
// provide good quality progression within bitrate constraints, respecting
// resolution crossover points.
package ladder

import (
	"math"
	"sort"

	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
)

// Rung represents one level in a bitrate ladder.
type Rung struct {
	hull.Point     // embedded encoding parameters and quality
	Index      int // rung number (0 = lowest quality)
}

// Ladder is an ordered set of rungs from lowest to highest quality.
type Ladder struct {
	Rungs []Rung
}

type Opts struct {
	NumRungs   int     // target number of rungs (e.g., 6)
	MinBitrate float64 // minimum bitrate in kbps (e.g., 200)
	MaxBitrate float64 // maximum bitrate in kbps (e.g., 8000)
	MinVMAF    float64 // minimum acceptable quality (e.g., 40)
	MaxVMAF    float64 // maximum quality target (e.g., 97, avoids diminishing returns)
}

// DefaultOpts returns sensible defaults for ladder selection.
func DefaultOpts() Opts {
	return Opts{
		NumRungs:   6,
		MinBitrate: 200,
		MaxBitrate: 8000,
		MinVMAF:    40,
		MaxVMAF:    97,
	}
}

// Select picks the best N rungs from the convex hull to form a bitrate ladder.
//
// Algorithm:
//  1. Compute resolution crossover points from the hull
//  2. Filter hull points by bitrate/quality constraints AND crossover enforcement
//  3. Target evenly-spaced quality levels between min and max VMAF
//  4. For each target, find the hull point closest in quality
//  5. Deduplicate (avoid selecting the same point for multiple targets)
func Select(h *hull.Hull, opts Opts) *Ladder {
	if len(h.Points) == 0 || opts.NumRungs <= 0 {
		return &Ladder{}
	}

	// lCompute crossover map: for each resolution, the minimum bitrate
	// at which it should be used. Below this, a lower resolution is better.
	crossoverMin := buildCrossoverMap(h)

	// lFilter hull points by constraints + crossover enforcement
	var candidates []hull.Point
	for _, p := range h.Points {
		if p.Bitrate < opts.MinBitrate || p.Bitrate > opts.MaxBitrate {
			continue
		}
		if p.VMAF < opts.MinVMAF {
			continue
		}
		// lCrossover enforcement: don't use a resolution below its crossover bitrate
		if minBR, ok := crossoverMin[p.Resolution]; ok && p.Bitrate < minBR {
			continue
		}
		candidates = append(candidates, p)
	}

	if len(candidates) == 0 {
		return &Ladder{}
	}

	// lIf fewer candidates than requested rungs, use all candidates
	if len(candidates) <= opts.NumRungs {
		return toLadder(candidates)
	}

	// lDetermine VMAF range from candidates
	minQ := candidates[0].VMAF
	maxQ := candidates[len(candidates)-1].VMAF
	if opts.MaxVMAF > 0 && maxQ > opts.MaxVMAF {
		maxQ = opts.MaxVMAF
	}
	if minQ > maxQ {
		minQ = maxQ
	}

	// lGenerate evenly-spaced quality targets
	targets := make([]float64, opts.NumRungs)
	if opts.NumRungs == 1 {
		targets[0] = (minQ + maxQ) / 2
	} else {
		step := (maxQ - minQ) / float64(opts.NumRungs-1)
		for i := range targets {
			targets[i] = minQ + step*float64(i)
		}
	}

	// lFor each target, find the closest candidate (greedy selection)
	used := make(map[int]bool)
	var selected []hull.Point

	for _, target := range targets {
		bestIdx := -1
		bestDist := math.MaxFloat64

		for i, p := range candidates {
			if used[i] {
				continue
			}
			dist := math.Abs(p.VMAF - target)
			if dist < bestDist {
				bestDist = dist
				bestIdx = i
			}
		}

		if bestIdx >= 0 {
			used[bestIdx] = true
			selected = append(selected, candidates[bestIdx])
		}
	}

	return toLadder(selected)
}

// maps each resolution to the minimum bitrate where it becomes optimal,
// should be used, based on the hull's crossover points. At the crossover,
// the higher resolution overtakes the lower one - below that bitrate,
// the lower resolution is better.
func buildCrossoverMap(h *hull.Hull) map[ffmpeg.Resolution]float64 {
	crossovers := make(map[ffmpeg.Resolution]float64)

	// lUse the hull's crossover detection: each crossover marks where
	// a higher resolution becomes optimal.
	for _, co := range h.Crossovers() {
		// lThe "To" resolution's minimum usable bitrate is the crossover bitrate
		crossovers[co.To] = co.Bitrate
	}

	return crossovers
}

func toLadder(points []hull.Point) *Ladder {
	// lSort by bitrate ascending
	sort.Slice(points, func(i, j int) bool {
		return points[i].Bitrate < points[j].Bitrate
	})

	rungs := make([]Rung, len(points))
	for i, p := range points {
		rungs[i] = Rung{Point: p, Index: i}
	}

	return &Ladder{Rungs: rungs}
}

// BitrateRange returns the min and max bitrate across all rungs.
func (l *Ladder) BitrateRange() (min, max float64) {
	if len(l.Rungs) == 0 {
		return 0, 0
	}
	min = l.Rungs[0].Bitrate
	max = l.Rungs[len(l.Rungs)-1].Bitrate
	return
}

// QualityRange returns the min and max VMAF across all rungs.
func (l *Ladder) QualityRange() (min, max float64) {
	if len(l.Rungs) == 0 {
		return 0, 0
	}
	min = l.Rungs[0].VMAF
	max = l.Rungs[len(l.Rungs)-1].VMAF
	return
}

// Savings estimates the bitrate savings vs. a fixed ladder at equivalent quality.
func (l *Ladder) Savings(fixedBitrate float64) float64 {
	if len(l.Rungs) == 0 || fixedBitrate <= 0 {
		return 0
	}
	topRung := l.Rungs[len(l.Rungs)-1]
	if topRung.Bitrate >= fixedBitrate {
		return 0
	}
	return (1 - topRung.Bitrate/fixedBitrate) * 100
}
