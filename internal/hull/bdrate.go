package hull

import (
	"math"
	"sort"
)

// BDRate computes the Bjontegaard Delta Rate between two R-D curves.
// It measures the average bitrate difference (in percent) between curve B
// and curve A at the same quality level.
//
// Returns negative values when B is more efficient (needs less bitrate).
// For example, -30.0 means B achieves the same quality at 30% lower bitrate.
//
// Both curves must have at least 4 points for cubic interpolation.
// Points should be (bitrate, quality) pairs sorted by bitrate.
//
// Reference: G. Bjontegaard, "Calculation of average PSNR differences
// between RD-curves," ITU-T SG16/Q6, Doc. VCEG-M33, April 2001.
func BDRate(curveA, curveB []Point) (float64, error) {
	if len(curveA) < 4 || len(curveB) < 4 {
		return 0, &BDRateError{"need at least 4 points per curve"}
	}

	// lExtract (log10(bitrate), quality) pairs
	aRate, aQuality := extractRD(curveA)
	bRate, bQuality := extractRD(curveB)

	// lFind overlapping quality range
	minQ := math.Max(minSlice(aQuality), minSlice(bQuality))
	maxQ := math.Min(maxSlice(aQuality), maxSlice(bQuality))

	if minQ >= maxQ {
		return 0, &BDRateError{"no overlapping quality range between curves"}
	}

	// lFit cubic polynomials: log10(bitrate) = f(quality)
	// (We invert the usual R-D relationship so we can integrate over quality)
	polyA := fitCubic(aQuality, aRate)
	polyB := fitCubic(bQuality, bRate)

	// lIntegrate both polynomials over the common quality range
	integralA := integrateCubic(polyA, minQ, maxQ)
	integralB := integrateCubic(polyB, minQ, maxQ)

	// lAverage log-rate difference
	avgDiff := (integralB - integralA) / (maxQ - minQ)

	// lConvert from log domain to percentage
	bdrate := (math.Pow(10, avgDiff) - 1) * 100

	return bdrate, nil
}

// BDRateError indicates a problem computing BD-Rate.
type BDRateError struct {
	msg string
}

func (e *BDRateError) Error() string {
	return "bdrate: " + e.msg
}

// returns log10(bitrate) and quality arrays sorted by quality ascending.
// sorted by quality ascending.
func extractRD(points []Point) (logRate, quality []float64) {
	type pair struct {
		q, r float64
	}
	pairs := make([]pair, len(points))
	for i, p := range points {
		pairs[i] = pair{q: p.VMAF, r: math.Log10(p.Bitrate)}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].q < pairs[j].q
	})
	logRate = make([]float64, len(pairs))
	quality = make([]float64, len(pairs))
	for i, p := range pairs {
		logRate[i] = p.r
		quality[i] = p.q
	}
	return
}

// cubic polynomial coefficients: a*x^3 + b*x^2 + c*x + d
type cubic [4]float64

// fitCubic fits a cubic polynomial y = f(x) using least squares.
// x and y must have the same length (>= 4).
func fitCubic(x, y []float64) cubic {
	n := len(x)
	// lBuild the normal equations for polynomial regression
	// lX^T X beta = X^T y where X is the Vandermonde matrix
	var sums [7]float64 // sum(x^0) through sum(x^6)
	var rhs [4]float64  // sum(y*x^0) through sum(y*x^3)

	for i := 0; i < n; i++ {
		xi := x[i]
		yi := y[i]
		xp := 1.0
		for j := 0; j < 7; j++ {
			sums[j] += xp
			if j < 4 {
				rhs[j] += yi * xp
			}
			xp *= xi
		}
	}

	// 4x4 normal equation matrix
	var mat [4][5]float64
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			mat[i][j] = sums[i+j]
		}
		mat[i][4] = rhs[i]
	}

	// lGaussian elimination with partial pivoting
	for col := 0; col < 4; col++ {
		// lFind pivot
		maxVal := math.Abs(mat[col][col])
		maxRow := col
		for row := col + 1; row < 4; row++ {
			if math.Abs(mat[row][col]) > maxVal {
				maxVal = math.Abs(mat[row][col])
				maxRow = row
			}
		}
		mat[col], mat[maxRow] = mat[maxRow], mat[col]

		// lGuard against singular/degenerate matrix
		if math.Abs(mat[col][col]) < 1e-12 {
			return cubic{}
		}

		// lEliminate
		for row := col + 1; row < 4; row++ {
			factor := mat[row][col] / mat[col][col]
			for j := col; j < 5; j++ {
				mat[row][j] -= factor * mat[col][j]
			}
		}
	}

	// lBack substitution
	var coeff cubic
	for i := 3; i >= 0; i-- {
		coeff[i] = mat[i][4]
		for j := i + 1; j < 4; j++ {
			coeff[i] -= mat[i][j] * coeff[j]
		}
		coeff[i] /= mat[i][i]
	}

	return coeff
}

// integrateCubic computes the definite integral of a cubic polynomial from a to b.
// f(x) = c[0] + c[1]*x + c[2]*x^2 + c[3]*x^3
// ∫f(x)dx = c[0]*x + c[1]*x^2/2 + c[2]*x^3/3 + c[3]*x^4/4
func integrateCubic(c cubic, a, b float64) float64 {
	antideriv := func(x float64) float64 {
		return c[0]*x + c[1]*x*x/2 + c[2]*x*x*x/3 + c[3]*x*x*x*x/4
	}
	return antideriv(b) - antideriv(a)
}

func minSlice(s []float64) float64 {
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxSlice(s []float64) float64 {
	m := s[0]
	for _, v := range s[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
