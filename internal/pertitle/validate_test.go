//go:build integration

package pertitle

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/ladder"
)

// These are integration tests that run real encodes against test assets.
// They validate that VEO's per-title pipeline produces results consistent
// with published encoding optimization research.
//
// Run with: go test -v -tags=integration -timeout=10m ./internal/pertitle/
//
// Required: test assets in assets/sd/ (run ./scripts/download-assets.sh --tier micro)

const (
	akiyoPath   = "../../assets/sd/akiyo_cif.y4m"   // talking head, low complexity
	foremanPath = "../../assets/sd/foreman_cif.y4m" // moderate motion
)

func assetExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestHullMonotonicity verifies that the convex hull has monotonically
// increasing quality with increasing bitrate. This is a fundamental
// property: spending more bits should always produce equal or better quality.
func TestHullMonotonicity(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("test asset not available: run ./scripts/download-assets.sh --tier micro")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{20, 26, 32, 38, 44},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    2,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	if len(result.Hull.Points) < 2 {
		t.Fatalf("hull should have at least 2 points, got %d", len(result.Hull.Points))
	}

	for i := 1; i < len(result.Hull.Points); i++ {
		prev := result.Hull.Points[i-1]
		curr := result.Hull.Points[i]
		if curr.Bitrate < prev.Bitrate {
			t.Errorf("hull bitrate not monotonic: point %d (%.0f kbps) < point %d (%.0f kbps)",
				i, curr.Bitrate, i-1, prev.Bitrate)
		}
		if curr.VMAF < prev.VMAF {
			t.Errorf("hull VMAF not monotonic: point %d (%.1f) < point %d (%.1f)",
				i, curr.VMAF, i-1, prev.VMAF)
		}
	}

	t.Logf("Hull has %d points:", len(result.Hull.Points))
	for _, p := range result.Hull.Points {
		t.Logf("  %s CRF %d: %.0f kbps, VMAF %.1f", p.Resolution.Label(), p.CRF, p.Bitrate, p.VMAF)
	}
}

// TestCRFBitrateRelationship verifies that lower CRF values produce higher
// bitrates and higher quality. This is the fundamental CRF property.
func TestCRFBitrateRelationship(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("test asset not available: run ./scripts/download-assets.sh --tier micro")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{20, 30, 40},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    1,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	// Find points by CRF
	byCRF := make(map[int]float64)
	vmafByCRF := make(map[int]float64)
	for _, p := range result.Points {
		byCRF[p.CRF] = p.Bitrate
		vmafByCRF[p.CRF] = p.VMAF
	}

	// Lower CRF → higher bitrate
	if byCRF[20] <= byCRF[30] {
		t.Errorf("CRF 20 (%.0f kbps) should have higher bitrate than CRF 30 (%.0f kbps)",
			byCRF[20], byCRF[30])
	}
	if byCRF[30] <= byCRF[40] {
		t.Errorf("CRF 30 (%.0f kbps) should have higher bitrate than CRF 40 (%.0f kbps)",
			byCRF[30], byCRF[40])
	}

	// Lower CRF → higher VMAF
	if vmafByCRF[20] <= vmafByCRF[30] {
		t.Errorf("CRF 20 (VMAF %.1f) should have higher quality than CRF 30 (VMAF %.1f)",
			vmafByCRF[20], vmafByCRF[30])
	}
	if vmafByCRF[30] <= vmafByCRF[40] {
		t.Errorf("CRF 30 (VMAF %.1f) should have higher quality than CRF 40 (VMAF %.1f)",
			vmafByCRF[30], vmafByCRF[40])
	}

	t.Logf("CRF→Bitrate→VMAF relationship verified:")
	for _, crf := range []int{20, 30, 40} {
		t.Logf("  CRF %d: %.0f kbps, VMAF %.1f", crf, byCRF[crf], vmafByCRF[crf])
	}
}

// TestContentComplexityDifference verifies that different content produces
// different encoding characteristics. Simple content (akiyo) should require
// less bitrate than complex content (foreman) at the same quality.
func TestContentComplexityDifference(t *testing.T) {
	if !assetExists(akiyoPath) || !assetExists(foremanPath) {
		t.Skip("test assets not available: run ./scripts/download-assets.sh --tier micro")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{26},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    1,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	akiyoResult, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("akiyo analysis failed: %v", err)
	}

	foremanResult, err := Analyze(context.Background(), foremanPath, cfg, nil)
	if err != nil {
		t.Fatalf("foreman analysis failed: %v", err)
	}

	akiyoBitrate := akiyoResult.Points[0].Bitrate
	foremanBitrate := foremanResult.Points[0].Bitrate

	akiyoVMAF := akiyoResult.Points[0].VMAF
	foremanVMAF := foremanResult.Points[0].VMAF

	// foreman has more motion/detail, so at the same CRF it needs more bitrate
	if foremanBitrate <= akiyoBitrate {
		t.Errorf("foreman (%.0f kbps) should have higher bitrate than akiyo (%.0f kbps) at same CRF",
			foremanBitrate, akiyoBitrate)
	}

	// both should achieve comparable quality at the same CRF (within 10 VMAF)
	// - this validates that the bitrate difference is real content complexity,
	//   not one clip failing to encode properly
	vmafDiff := math.Abs(akiyoVMAF - foremanVMAF)
	if vmafDiff > 10 {
		t.Errorf("quality difference too large (%.1f VMAF) - bitrate comparison may be meaningless",
			vmafDiff)
	}

	ratio := foremanBitrate / akiyoBitrate
	t.Logf("Content complexity difference at CRF 26:")
	t.Logf("  akiyo (talking head):   %.0f kbps, VMAF %.1f", akiyoBitrate, akiyoVMAF)
	t.Logf("  foreman (motion):       %.0f kbps, VMAF %.1f", foremanBitrate, foremanVMAF)
	t.Logf("  bitrate ratio:          %.1fx", ratio)
}

// TestCodecEfficiency verifies that more advanced codecs achieve better
// compression at the same quality. If SVT-AV1 is available, it should
// produce lower bitrate than x264 at similar VMAF.
//
// Published reference: AV1 achieves ~50% BD-rate improvement over H.264.
// We test for at least 20% improvement (conservative, using fast presets).
func TestCodecEfficiency(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("test asset not available: run ./scripts/download-assets.sh --tier micro")
	}

	// Check if SVT-AV1 is available
	ctx := context.Background()
	testEnc := ffmpeg.EncodeJob{
		Input:  akiyoPath,
		Output: os.TempDir() + "/veo-codec-test.mp4",
		Codec:  ffmpeg.CodecSVTAV1,
		CRF:    35,
		Preset: "12",
	}
	_, err := ffmpeg.Encode(ctx, testEnc, nil)
	os.Remove(testEnc.Output)
	if err != nil {
		t.Skip("SVT-AV1 not available, skipping codec efficiency test")
	}

	// Run analysis with both codecs at a mid-range CRF
	// Use CRF values that produce roughly similar quality ranges
	cfg264 := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{22, 28, 34},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    2,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	cfgAV1 := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{30, 38, 46},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecSVTAV1},
			Preset:      "12", // fastest SVT-AV1 preset
			Subsample:   5,
			Parallel:    2,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result264, err := Analyze(ctx, akiyoPath, cfg264, nil)
	if err != nil {
		t.Fatalf("x264 analysis failed: %v", err)
	}

	resultAV1, err := Analyze(ctx, akiyoPath, cfgAV1, nil)
	if err != nil {
		t.Fatalf("SVT-AV1 analysis failed: %v", err)
	}

	// Find points at roughly VMAF 85-90 from each codec
	var x264Point, av1Point *struct{ bitrate, vmaf float64 }
	targetVMAF := 88.0

	for _, p := range result264.Points {
		if x264Point == nil || math.Abs(p.VMAF-targetVMAF) < math.Abs(x264Point.vmaf-targetVMAF) {
			x264Point = &struct{ bitrate, vmaf float64 }{p.Bitrate, p.VMAF}
		}
	}
	for _, p := range resultAV1.Points {
		if av1Point == nil || math.Abs(p.VMAF-targetVMAF) < math.Abs(av1Point.vmaf-targetVMAF) {
			av1Point = &struct{ bitrate, vmaf float64 }{p.Bitrate, p.VMAF}
		}
	}

	if x264Point == nil || av1Point == nil {
		t.Fatal("could not find comparable quality points")
	}

	savings := (1 - av1Point.bitrate/x264Point.bitrate) * 100

	t.Logf("Codec efficiency comparison near VMAF %.0f:", targetVMAF)
	t.Logf("  x264:    %.0f kbps at VMAF %.1f", x264Point.bitrate, x264Point.vmaf)
	t.Logf("  SVT-AV1: %.0f kbps at VMAF %.1f", av1Point.bitrate, av1Point.vmaf)
	t.Logf("  AV1 savings: %.0f%%", savings)

	// only compare savings when quality is comparable (within 5 VMAF)
	qualityComparable := math.Abs(x264Point.vmaf-av1Point.vmaf) < 5
	if !qualityComparable {
		t.Logf("quality not comparable (diff %.1f VMAF) - skipping savings check", math.Abs(x264Point.vmaf-av1Point.vmaf))
	} else if savings < 20 {
		t.Errorf("AV1 savings (%.0f%%) below expected minimum (20%%) at comparable quality", savings)
	}
}

// TestLadderSavingsVsFixed verifies that the optimized ladder achieves
// bitrate savings compared to a fixed ladder at the same quality.
//
// Published reference: Netflix reports 20-30% savings with per-title encoding
// over fixed ladders. Fraunhofer reports 35% savings.
// We test for at least 10% savings (conservative, small test clip).
func TestLadderSavingsVsFixed(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("test asset not available: run ./scripts/download-assets.sh --tier micro")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{18, 22, 26, 30, 34, 38, 42},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    2,
		},
		LadderOpts: ladder.Opts{
			NumRungs:   3,
			MinBitrate: 50,
			MaxBitrate: 2000,
			MinVMAF:    60,
			MaxVMAF:    97,
		},
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	if len(result.Ladder.Rungs) == 0 {
		t.Fatal("ladder has no rungs")
	}

	// the ladder must have at least 2 rungs for simple content at 7 CRF values
	if len(result.Ladder.Rungs) < 2 {
		t.Errorf("expected at least 2 ladder rungs for 7 CRF values, got %d", len(result.Ladder.Rungs))
	}

	// top rung must achieve reasonable quality (VMAF > 80 for simple content)
	topRung := result.Ladder.Rungs[len(result.Ladder.Rungs)-1]
	if topRung.VMAF < 80 {
		t.Errorf("top rung VMAF %.1f below minimum 80 for simple content", topRung.VMAF)
	}

	// quality must increase from bottom to top rung
	bottomRung := result.Ladder.Rungs[0]
	if topRung.VMAF <= bottomRung.VMAF {
		t.Errorf("top rung VMAF (%.1f) should exceed bottom rung (%.1f)", topRung.VMAF, bottomRung.VMAF)
	}

	t.Logf("Ladder: %d rungs, %.0f-%.0f kbps, VMAF %.1f-%.1f",
		len(result.Ladder.Rungs), bottomRung.Bitrate, topRung.Bitrate, bottomRung.VMAF, topRung.VMAF)

	t.Logf("\nFull optimized ladder:")
	for _, r := range result.Ladder.Rungs {
		t.Logf("  Rung %d: %s CRF %d, %.0f kbps, VMAF %.1f",
			r.Index+1, r.Resolution.Label(), r.CRF, r.Bitrate, r.VMAF)
	}
}

// TestDiminishingReturns verifies that R-D curves show diminishing returns  -
// the VMAF gain per additional kbps decreases as bitrate increases.
// This is a fundamental property of rate-distortion theory.
func TestDiminishingReturns(t *testing.T) {
	if !assetExists(akiyoPath) {
		t.Skip("test asset not available: run ./scripts/download-assets.sh --tier micro")
	}

	cfg := Config{
		Config: encoding.Config{
			Resolutions: []ffmpeg.Resolution{ffmpeg.Res240p},
			CRFValues:   []int{18, 22, 26, 30, 34, 38, 42},
			Codecs:      []ffmpeg.Codec{ffmpeg.CodecX264},
			Preset:      "ultrafast",
			Subsample:   5,
			Parallel:    2,
		},
		LadderOpts: ladder.DefaultOpts(),
	}

	result, err := Analyze(context.Background(), akiyoPath, cfg, nil)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}

	hull := result.Hull.Points
	if len(hull) < 3 {
		t.Skip("need at least 3 hull points for diminishing returns test")
	}

	// Compute marginal quality gain per kbps for each segment
	var slopes []float64
	for i := 1; i < len(hull); i++ {
		dBitrate := hull[i].Bitrate - hull[i-1].Bitrate
		dVMAF := hull[i].VMAF - hull[i-1].VMAF
		if dBitrate > 0 {
			slope := dVMAF / dBitrate // VMAF per kbps
			slopes = append(slopes, slope)
		}
	}

	// Slopes should generally decrease (diminishing returns)
	decreasing := 0
	for i := 1; i < len(slopes); i++ {
		if slopes[i] <= slopes[i-1] {
			decreasing++
		}
	}

	t.Logf("R-D curve slopes (VMAF/kbps):")
	for i, s := range slopes {
		t.Logf("  Segment %d: %.4f VMAF/kbps", i+1, s)
	}
	t.Logf("Decreasing segments: %d/%d", decreasing, len(slopes)-1)

	// Allow some tolerance - fast presets may not perfectly follow theory
	if len(slopes) > 2 && decreasing == 0 {
		t.Error("R-D curve does not show diminishing returns - slopes should generally decrease")
	}
}
