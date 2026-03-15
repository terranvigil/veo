package ladder

import "github.com/terranvigil/veo/internal/ffmpeg"

// FixedLadder represents a fixed (non-optimized) bitrate ladder for comparison.
type FixedLadder struct {
	Name  string
	Rungs []FixedRung
}

type FixedRung struct {
	Resolution ffmpeg.Resolution
	Bitrate    float64 // kbps
}

// NetflixOld returns Netflix's original fixed bitrate ladder from their
// 2015 per-title encoding blog post. This was the "one-size-fits-all"
// ladder used before per-title optimization.
//
// Source: https://netflixtechblog.com/per-title-encode-optimization-7e99442b62a2
func NetflixOld() FixedLadder {
	return FixedLadder{
		Name: "Netflix Fixed (2015)",
		Rungs: []FixedRung{
			{Resolution: ffmpeg.Resolution{Width: 320, Height: 240}, Bitrate: 235},
			{Resolution: ffmpeg.Resolution{Width: 384, Height: 288}, Bitrate: 375},
			{Resolution: ffmpeg.Resolution{Width: 512, Height: 384}, Bitrate: 560},
			{Resolution: ffmpeg.Resolution{Width: 512, Height: 384}, Bitrate: 750},
			{Resolution: ffmpeg.Resolution{Width: 640, Height: 480}, Bitrate: 1050},
			{Resolution: ffmpeg.Resolution{Width: 720, Height: 480}, Bitrate: 1750},
			{Resolution: ffmpeg.Resolution{Width: 1280, Height: 720}, Bitrate: 2350},
			{Resolution: ffmpeg.Resolution{Width: 1280, Height: 720}, Bitrate: 3000},
			{Resolution: ffmpeg.Resolution{Width: 1920, Height: 1080}, Bitrate: 4300},
			{Resolution: ffmpeg.Resolution{Width: 1920, Height: 1080}, Bitrate: 5800},
		},
	}
}

// AppleHLS returns Apple's HLS encoding recommendations (approximate, 2024 update).
// These are guideline bitrates, not strict requirements.
func AppleHLS() FixedLadder {
	return FixedLadder{
		Name: "Apple HLS (2024)",
		Rungs: []FixedRung{
			{Resolution: ffmpeg.Resolution{Width: 416, Height: 234}, Bitrate: 145},
			{Resolution: ffmpeg.Resolution{Width: 640, Height: 360}, Bitrate: 365},
			{Resolution: ffmpeg.Resolution{Width: 768, Height: 432}, Bitrate: 730},
			{Resolution: ffmpeg.Resolution{Width: 960, Height: 540}, Bitrate: 1100},
			{Resolution: ffmpeg.Resolution{Width: 1280, Height: 720}, Bitrate: 2000},
			{Resolution: ffmpeg.Resolution{Width: 1280, Height: 720}, Bitrate: 3000},
			{Resolution: ffmpeg.Resolution{Width: 1920, Height: 1080}, Bitrate: 4500},
			{Resolution: ffmpeg.Resolution{Width: 1920, Height: 1080}, Bitrate: 6000},
			{Resolution: ffmpeg.Resolution{Width: 1920, Height: 1080}, Bitrate: 7800},
		},
	}
}

// TotalBitrate returns the sum of all rung bitrates in the fixed ladder.
func (f FixedLadder) TotalBitrate() float64 {
	var total float64
	for _, r := range f.Rungs {
		total += r.Bitrate
	}
	return total
}

// TopBitrate returns the highest bitrate rung.
func (f FixedLadder) TopBitrate() float64 {
	if len(f.Rungs) == 0 {
		return 0
	}
	return f.Rungs[len(f.Rungs)-1].Bitrate
}

// Compare computes savings of the optimized ladder vs this fixed ladder.
// Returns savings percentage at comparable quality tiers.
type CompareResult struct {
	FixedName      string
	FixedRungs     int
	OptimizedRungs int
	// lFor each fixed rung, what quality does the optimized ladder achieve
	// at the same or lower bitrate?
	RungComparisons []RungComparison
	// lOverall: average bitrate savings at comparable quality
	AvgSavingsPercent float64
}

type RungComparison struct {
	FixedRes     ffmpeg.Resolution
	FixedBitrate float64
	OptRes       ffmpeg.Resolution
	OptBitrate   float64
	OptVMAF      float64
	OptCRF       int
	Savings      float64 // percentage savings (positive = optimized uses less)
}
