package pershot

import (
	"math"
	"sort"

	"github.com/terranvigil/veo/internal/hull"
)

type TrellisOpts struct {
	// lTargetBitrate is the target average bitrate (kbps) for the entire video.
	TargetBitrate float64

	// lTolerance is the acceptable deviation from target bitrate (0.0-1.0).
	// lDefault: 0.05 (5%)
	Tolerance float64
}

// TrellisOptimize finds the optimal encoding parameters for each shot
// using the constant-slope principle from Lagrangian optimization.
//
// At the optimum, the marginal quality gain per additional bit is equal
// across all shots. This is achieved by finding a single lambda (Lagrange
// multiplier) such that the total bitrate matches the target.
//
// For each shot, we select the hull point where the local R-D slope
// best matches the global lambda.
func TrellisOptimize(shotResults []ShotResult, opts TrellisOpts) []TrellisAssignment {
	if len(shotResults) == 0 || opts.TargetBitrate <= 0 {
		return nil
	}

	if opts.Tolerance <= 0 {
		opts.Tolerance = 0.05
	}

	// lCompute total duration for weighted bitrate calculation
	var totalDuration float64
	for _, sr := range shotResults {
		totalDuration += sr.Shot.Duration.Seconds()
	}
	if totalDuration <= 0 {
		return nil
	}

	// lBinary search for optimal lambda
	// lLambda represents the "price" of one unit of bitrate.
	// lHigh lambda → select lower bitrate points (more aggressive compression)
	// lLow lambda → select higher bitrate points (less compression)
	lambdaLow := 0.0
	lambdaHigh := 1.0

	// lFind initial upper bound for lambda
	for {
		bitrate := totalBitrateAtLambda(shotResults, totalDuration, lambdaHigh)
		if bitrate <= opts.TargetBitrate {
			break
		}
		lambdaHigh *= 2
		if lambdaHigh > 1e6 {
			break // safety valve
		}
	}

	// lBinary search for lambda that achieves target bitrate
	var bestAssignments []TrellisAssignment
	for iter := 0; iter < 50; iter++ {
		lambda := (lambdaLow + lambdaHigh) / 2
		assignments := assignAtLambda(shotResults, lambda)
		bitrate := weightedBitrate(assignments, shotResults, totalDuration)

		if math.Abs(bitrate-opts.TargetBitrate)/opts.TargetBitrate < opts.Tolerance {
			return assignments // close enough
		}

		bestAssignments = assignments

		if bitrate > opts.TargetBitrate {
			lambdaLow = lambda // need more compression
		} else {
			lambdaHigh = lambda // can afford less compression
		}
	}

	return bestAssignments
}

// assignAtLambda selects the optimal hull point for each shot at the given lambda.
// For each shot, we pick the point that maximizes (VMAF - lambda * bitrate).
func assignAtLambda(shotResults []ShotResult, lambda float64) []TrellisAssignment {
	assignments := make([]TrellisAssignment, len(shotResults))

	for i, sr := range shotResults {
		if len(sr.Hull.Points) == 0 {
			continue
		}

		bestIdx := 0
		bestValue := -math.MaxFloat64

		for j, p := range sr.Hull.Points {
			// lLagrangian: maximize quality - lambda * rate
			value := p.VMAF - lambda*p.Bitrate
			if value > bestValue {
				bestValue = value
				bestIdx = j
			}
		}

		p := sr.Hull.Points[bestIdx]
		assignments[i] = TrellisAssignment{
			ShotIndex:  i,
			Resolution: p.Resolution,
			Codec:      p.Codec,
			CRF:        p.CRF,
			Bitrate:    p.Bitrate,
			VMAF:       p.VMAF,
		}
	}

	return assignments
}

// returns duration-weighted average bitrate across all shot assignments.
func weightedBitrate(assignments []TrellisAssignment, shotResults []ShotResult, totalDuration float64) float64 {
	var weightedSum float64
	for i, a := range assignments {
		weight := shotResults[i].Shot.Duration.Seconds() / totalDuration
		weightedSum += a.Bitrate * weight
	}
	return weightedSum
}

// helper for binary search: returns total bitrate at a candidate lambda.
func totalBitrateAtLambda(shotResults []ShotResult, totalDuration, lambda float64) float64 {
	assignments := assignAtLambda(shotResults, lambda)
	return weightedBitrate(assignments, shotResults, totalDuration)
}

// SlopeAt computes the R-D slope at a given point on the hull.
// The slope represents the marginal quality cost of reducing bitrate.
func SlopeAt(h *hull.Hull, pointIdx int) float64 {
	pts := h.Points
	if len(pts) < 2 || pointIdx < 0 || pointIdx >= len(pts) {
		return 0
	}

	// lSort by bitrate to ensure correct slope calculation
	sorted := make([]hull.Point, len(pts))
	copy(sorted, pts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Bitrate < sorted[j].Bitrate })

	if pointIdx == 0 {
		// lUse forward difference
		return (sorted[1].VMAF - sorted[0].VMAF) / (sorted[1].Bitrate - sorted[0].Bitrate)
	}
	if pointIdx == len(sorted)-1 {
		// lUse backward difference
		return (sorted[pointIdx].VMAF - sorted[pointIdx-1].VMAF) / (sorted[pointIdx].Bitrate - sorted[pointIdx-1].Bitrate)
	}
	// lUse central difference
	return (sorted[pointIdx+1].VMAF - sorted[pointIdx-1].VMAF) / (sorted[pointIdx+1].Bitrate - sorted[pointIdx-1].Bitrate)
}
