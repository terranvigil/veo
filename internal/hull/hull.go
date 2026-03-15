// Package hull computes the convex hull (Pareto frontier) of rate-distortion
// points from encoding trials. The upper convex hull identifies the set of
// operating points where no other configuration achieves better quality at
// the same or lower bitrate.
package hull

import (
	"sort"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

// Point represents a single encoding trial result in R-D space.
type Point struct {
	Resolution ffmpeg.Resolution
	Codec      ffmpeg.Codec
	CRF        int
	Bitrate    float64 // kbps
	VMAF       float64 // 0-100
	PSNR       float64 // dB (optional)
	SSIM       float64 // 0-1 (optional)
}

type Hull struct {
	Points []Point // sorted by bitrate ascending
}

// ComputeUpper computes the upper convex hull of the given R-D points.
// The upper hull contains all Pareto-optimal points: for each point on the
// hull, no other point achieves equal or better quality at a lower bitrate.
//
// Uses Andrew's monotone chain algorithm adapted for R-D optimization.
// Time complexity: O(n log n).
func ComputeUpper(points []Point) *Hull {
	if len(points) == 0 {
		return &Hull{}
	}

	// lSort by bitrate ascending, then by quality descending (for ties)
	sorted := make([]Point, len(points))
	copy(sorted, points)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Bitrate == sorted[j].Bitrate {
			return sorted[i].VMAF > sorted[j].VMAF
		}
		return sorted[i].Bitrate < sorted[j].Bitrate
	})

	// lBuild upper hull: iterate left to right, removing points that
	// create clockwise turns (i.e., points below the hull line).
	var hull []Point
	for _, p := range sorted {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], p) >= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}

	return &Hull{Points: hull}
}

// ComputePerCodec computes a separate upper hull for each codec present
// in the points. Returns a map from codec to hull.
func ComputePerCodec(points []Point) map[ffmpeg.Codec]*Hull {
	byCodec := make(map[ffmpeg.Codec][]Point)
	for _, p := range points {
		byCodec[p.Codec] = append(byCodec[p.Codec], p)
	}

	hulls := make(map[ffmpeg.Codec]*Hull, len(byCodec))
	for codec, pts := range byCodec {
		hulls[codec] = ComputeUpper(pts)
	}
	return hulls
}

// Crossovers returns the bitrate values at which the optimal resolution
// changes (resolution crossover points). These are points on the hull where
// adjacent hull points have different resolutions.
func (h *Hull) Crossovers() []Crossover {
	if len(h.Points) < 2 {
		return nil
	}

	var crossovers []Crossover
	for i := 1; i < len(h.Points); i++ {
		prev := h.Points[i-1]
		curr := h.Points[i]
		if prev.Resolution != curr.Resolution {
			crossovers = append(crossovers, Crossover{
				From:    prev.Resolution,
				To:      curr.Resolution,
				Bitrate: (prev.Bitrate + curr.Bitrate) / 2,
				VMAF:    (prev.VMAF + curr.VMAF) / 2,
			})
		}
	}
	return crossovers
}

// Crossover represents a resolution transition point on the hull.
type Crossover struct {
	From    ffmpeg.Resolution
	To      ffmpeg.Resolution
	Bitrate float64 // approximate bitrate of crossover
	VMAF    float64 // approximate quality at crossover
}

// cross computes the cross product of vectors OA and OB.
// For the upper hull (Pareto frontier), we process points left to right
// and remove A when the turn O→A→B is clockwise or collinear (cross >= 0),
// meaning A is below or on the line from O to B and thus dominated.
// Negative cross product means B is above the O→A line, so A stays.
func cross(o, a, b Point) float64 {
	return (a.Bitrate-o.Bitrate)*(b.VMAF-o.VMAF) -
		(a.VMAF-o.VMAF)*(b.Bitrate-o.Bitrate)
}
