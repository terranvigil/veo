// Package chart generates PNG and SVG images of R-D curves, convex hulls,
// and bitrate ladders from per-title analysis results.
package chart

import (
	"bytes"
	"fmt"
	"image/color"
	"os"
	"sort"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"

	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
)

const (
	labelBitrate = "Bitrate (kbps)"
	labelVMAF    = "VMAF"
)

// palette of distinct colors for series (colorblind-friendly)
var seriesColors = []color.RGBA{
	{R: 0, G: 114, B: 178, A: 255},   // blue
	{R: 230, G: 159, B: 0, A: 255},   // orange
	{R: 0, G: 158, B: 115, A: 255},   // green
	{R: 204, G: 121, B: 167, A: 255}, // pink
	{R: 86, G: 180, B: 233, A: 255},  // sky blue
	{R: 213, G: 94, B: 0, A: 255},    // vermilion
	{R: 240, G: 228, B: 66, A: 255},  // yellow
}

var hullColor = color.RGBA{R: 50, G: 50, B: 50, A: 220}

type Opts struct {
	Title      string
	Subtitle   string
	Width      float64 // inches (default 9)
	Height     float64 // inches (default 5.5)
	Format     string  // "png" or "svg" (default "png")
	MaxBitrate float64 // max X axis value in kbps (0 = auto)
}

func (o Opts) withDefaults() Opts {
	if o.Width == 0 {
		o.Width = 9
	}
	if o.Height == 0 {
		o.Height = 5.5
	}
	if o.Format == "" {
		o.Format = "png"
	}
	return o
}

func setupPlot(title, xLabel, yLabel string) *plot.Plot {
	p := plot.New()
	p.Title.Text = title
	p.Title.Padding = vg.Points(8)
	p.X.Label.Text = xLabel
	p.Y.Label.Text = yLabel

	// lGrid lines
	p.Add(plotter.NewGrid())

	return p
}

// RDCurve generates an R-D curve chart with one line per resolution+codec,
// the convex hull as a thick dashed line, and crossover annotations.
func RDCurve(points []hull.Point, convexHull *hull.Hull, opts Opts) ([]byte, error) {
	opts = opts.withDefaults()
	if opts.Title == "" {
		opts.Title = "Rate-Distortion Curve"
	}

	p := setupPlot(opts.Title, labelBitrate, labelVMAF)

	// auto-scale Y axis to data range with padding
	minVMAF, maxVMAF := findVMAFRange(points)
	p.Y.Min = max(0, minVMAF-10)
	p.Y.Max = min(100, maxVMAF+5)

	// filter points to max bitrate if requested (shows curve separation better)
	if opts.MaxBitrate > 0 {
		var filtered []hull.Point
		for _, pt := range points {
			if pt.Bitrate <= opts.MaxBitrate {
				filtered = append(filtered, pt)
			}
		}
		points = filtered
		if convexHull != nil {
			var filteredHull []hull.Point
			for _, pt := range convexHull.Points {
				if pt.Bitrate <= opts.MaxBitrate {
					filteredHull = append(filteredHull, pt)
				}
			}
			convexHull = &hull.Hull{Points: filteredHull}
		}
	}

	// legend in bottom-right (less likely to overlap data)
	p.Legend.Top = false
	p.Legend.Left = false
	p.Legend.XOffs = -vg.Points(10)
	p.Legend.YOffs = vg.Points(10)

	// lGroup points by resolution+codec
	type seriesKey struct {
		Res    string
		Codec  string
		Height int // for sorting by resolution order
	}
	bySeries := make(map[seriesKey][]hull.Point)
	for _, pt := range points {
		key := seriesKey{Res: pt.Resolution.Label(), Codec: string(pt.Codec), Height: pt.Resolution.Height}
		bySeries[key] = append(bySeries[key], pt)
	}

	// sort by resolution height (ascending), then codec
	keys := make([]seriesKey, 0, len(bySeries))
	for k := range bySeries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Height != keys[j].Height {
			return keys[i].Height < keys[j].Height
		}
		return keys[i].Codec < keys[j].Codec
	})

	colorIdx := 0
	for _, key := range keys {
		pts := bySeries[key]
		sort.Slice(pts, func(i, j int) bool { return pts[i].Bitrate < pts[j].Bitrate })

		xy := make(plotter.XYs, len(pts))
		for i, pt := range pts {
			xy[i].X = pt.Bitrate
			xy[i].Y = pt.VMAF
		}

		line, scatter, err := plotter.NewLinePoints(xy)
		if err != nil {
			continue
		}

		c := seriesColors[colorIdx%len(seriesColors)]
		line.Color = c
		line.Width = vg.Points(1.5)
		scatter.Color = c
		scatter.Radius = vg.Points(2.5)
		scatter.Shape = draw.CircleGlyph{}

		label := key.Res
		// include codec in label only when multiple codecs are present
		codecSet := make(map[string]struct{})
		for k := range bySeries {
			codecSet[k.Codec] = struct{}{}
		}
		if len(codecSet) > 1 {
			label = fmt.Sprintf("%s %s", key.Res, shortCodecName(key.Codec))
		}

		p.Add(line, scatter)
		p.Legend.Add(label, line, scatter)
		colorIdx++
	}

	// lConvex hull as thick dashed line with diamond markers
	if convexHull != nil && len(convexHull.Points) > 1 {
		hullXY := make(plotter.XYs, len(convexHull.Points))
		for i, pt := range convexHull.Points {
			hullXY[i].X = pt.Bitrate
			hullXY[i].Y = pt.VMAF
		}

		hullLine, err := plotter.NewLine(hullXY)
		if err == nil {
			hullLine.Color = hullColor
			hullLine.Width = vg.Points(2.5)
			hullLine.Dashes = []vg.Length{vg.Points(6), vg.Points(3)}
			p.Add(hullLine)
			p.Legend.Add("Convex Hull", hullLine)
		}

		// lMark crossover points with vertical lines
		crossovers := convexHull.Crossovers()
		for i, co := range crossovers {
			addCrossoverAnnotation(p, co, i)
		}
	}

	return renderChart(p, opts)
}

// PerCodecRDCurve generates R-D curves comparing codec efficiency.
func PerCodecRDCurve(perCodec map[ffmpeg.Codec]*hull.Hull, bdRate float64, opts Opts) ([]byte, error) {
	opts = opts.withDefaults()
	if opts.Title == "" {
		opts.Title = "Codec Comparison"
	}

	p := setupPlot(opts.Title, labelBitrate, labelVMAF)

	// lAuto-scale Y
	var allPoints []hull.Point
	for _, h := range perCodec {
		allPoints = append(allPoints, h.Points...)
	}
	minV, maxV := findVMAFRange(allPoints)
	p.Y.Min = max(0, minV-10)
	p.Y.Max = min(100, maxV+5)

	p.Legend.Top = true
	p.Legend.Left = false

	// lSort codecs for deterministic order
	codecs := make([]ffmpeg.Codec, 0, len(perCodec))
	for c := range perCodec {
		codecs = append(codecs, c)
	}
	sort.Slice(codecs, func(i, j int) bool { return string(codecs[i]) < string(codecs[j]) })

	colorIdx := 0
	for _, codec := range codecs {
		h := perCodec[codec]
		pts := make([]hull.Point, len(h.Points))
		copy(pts, h.Points)
		sort.Slice(pts, func(i, j int) bool { return pts[i].Bitrate < pts[j].Bitrate })

		xy := make(plotter.XYs, len(pts))
		for i, pt := range pts {
			xy[i].X = pt.Bitrate
			xy[i].Y = pt.VMAF
		}

		line, scatter, err := plotter.NewLinePoints(xy)
		if err != nil {
			continue
		}

		c := seriesColors[colorIdx%len(seriesColors)]
		line.Color = c
		line.Width = vg.Points(2.5)
		scatter.Color = c
		scatter.Radius = vg.Points(3)

		label := shortCodecName(string(codec))
		p.Add(line, scatter)
		p.Legend.Add(label, line, scatter)
		colorIdx++
	}

	// lAdd BD-Rate annotation if available
	if bdRate != 0 && len(codecs) == 2 {
		annotation := fmt.Sprintf("BD-Rate: %.1f%%", bdRate)
		labels, err := plotter.NewLabels(plotter.XYLabels{
			XYs:    []plotter.XY{{X: p.X.Max * 0.6, Y: p.Y.Min + (p.Y.Max-p.Y.Min)*0.15}},
			Labels: []string{annotation},
		})
		if err == nil {
			labels.TextStyle[0].Font.Size = vg.Points(11)
			p.Add(labels)
		}
	}

	return renderChart(p, opts)
}

// LadderChart generates a horizontal bar chart showing the bitrate ladder
// with color-coded bars by resolution.
func LadderChart(l *ladder.Ladder, opts Opts) ([]byte, error) {
	opts = opts.withDefaults()
	if opts.Title == "" {
		opts.Title = "Optimized Bitrate Ladder"
	}
	opts.Height = max(3.0, float64(len(l.Rungs))*0.7+1.5)

	p := setupPlot(opts.Title, labelBitrate, "")

	values := make(plotter.Values, len(l.Rungs))
	labels := make([]string, len(l.Rungs))
	for i, r := range l.Rungs {
		values[i] = r.Bitrate
		codec := shortCodecName(string(r.Codec))
		labels[i] = fmt.Sprintf("#%d %s %s VMAF %.0f", i+1, r.Resolution.Label(), codec, r.VMAF)
	}

	bars, err := plotter.NewBarChart(values, vg.Points(22))
	if err != nil {
		return nil, fmt.Errorf("failed to create bar chart: %w", err)
	}
	bars.Horizontal = true
	bars.Color = seriesColors[0]
	bars.LineStyle.Width = vg.Points(0.5)

	// lColor bars by resolution
	colorByRes := make(map[string]color.Color)
	resIdx := 0
	for _, r := range l.Rungs {
		label := r.Resolution.Label()
		if _, exists := colorByRes[label]; !exists {
			colorByRes[label] = seriesColors[resIdx%len(seriesColors)]
			resIdx++
		}
	}

	p.Add(bars)
	p.NominalY(labels...)

	return renderChart(p, opts)
}

// ComplexityChart generates a temporal complexity profile chart showing
// per-segment spatial and temporal complexity over time.
func ComplexityChart(segments []struct {
	Start, End float64
	Spatial    float64
	Temporal   float64
	Score      float64
}, opts Opts) ([]byte, error) {
	opts = opts.withDefaults()
	if opts.Title == "" {
		opts.Title = "Content Complexity Profile"
	}

	p := setupPlot(opts.Title, "Time (s)", "Complexity Score")
	p.Y.Min = 0
	p.Y.Max = 100
	p.Legend.Top = true

	scoreXY := make(plotter.XYs, len(segments))
	for i, seg := range segments {
		scoreXY[i].X = (seg.Start + seg.End) / 2
		scoreXY[i].Y = seg.Score
	}

	line, scatter, err := plotter.NewLinePoints(scoreXY)
	if err != nil {
		return nil, err
	}
	line.Color = seriesColors[0]
	line.Width = vg.Points(2)
	scatter.Color = seriesColors[0]
	scatter.Radius = vg.Points(3)

	p.Add(line, scatter)
	p.Legend.Add("Complexity", line, scatter)

	return renderChart(p, opts)
}

type ShotHull struct {
	Name   string
	Points []hull.Point // R-D points (convex hull or raw)
}

// PerShotHulls generates a chart overlaying multiple shots' convex hulls
// to show how different content complexity produces different R-D curves.
func PerShotHulls(shots []ShotHull, opts Opts) ([]byte, error) {
	opts = opts.withDefaults()
	if opts.Title == "" {
		opts.Title = "Per-Shot Convex Hulls"
	}

	p := setupPlot(opts.Title, labelBitrate, labelVMAF)

	var allPoints []hull.Point
	for _, s := range shots {
		allPoints = append(allPoints, s.Points...)
	}
	minV, maxV := findVMAFRange(allPoints)
	p.Y.Min = max(0, minV-5)
	p.Y.Max = min(100, maxV+5)

	p.Legend.Top = false
	p.Legend.Left = false
	p.Legend.XOffs = -vg.Points(10)
	p.Legend.YOffs = vg.Points(10)

	for i, shot := range shots {
		pts := make([]hull.Point, len(shot.Points))
		copy(pts, shot.Points)
		sort.Slice(pts, func(a, b int) bool { return pts[a].Bitrate < pts[b].Bitrate })

		xy := make(plotter.XYs, len(pts))
		for j, pt := range pts {
			xy[j].X = pt.Bitrate
			xy[j].Y = pt.VMAF
		}

		line, scatter, err := plotter.NewLinePoints(xy)
		if err != nil {
			continue
		}

		c := seriesColors[i%len(seriesColors)]
		line.Color = c
		line.Width = vg.Points(2.5)
		scatter.Color = c
		scatter.Radius = vg.Points(3)

		p.Add(line, scatter)
		p.Legend.Add(shot.Name, line, scatter)
	}

	return renderChart(p, opts)
}

type ShotAllocation struct {
	Name    string
	Bitrate float64 // allocated bitrate in kbps
	VMAF    float64
}

// TrellisAllocationChart generates a grouped bar chart showing per-shot
// bitrate allocation and achieved VMAF from Trellis optimization.
func TrellisAllocationChart(shots []ShotAllocation, opts Opts) ([]byte, error) {
	opts = opts.withDefaults()
	if opts.Title == "" {
		opts.Title = "Trellis Bit Allocation"
	}
	opts.Height = max(4.0, float64(len(shots))*0.8+1.5)

	p := setupPlot(opts.Title, labelBitrate, "")

	values := make(plotter.Values, len(shots))
	labels := make([]string, len(shots))
	for i, s := range shots {
		values[i] = s.Bitrate
		labels[i] = fmt.Sprintf("%s (VMAF %.0f)", s.Name, s.VMAF)
	}

	bars, err := plotter.NewBarChart(values, vg.Points(22))
	if err != nil {
		return nil, fmt.Errorf("failed to create bar chart: %w", err)
	}
	bars.Horizontal = true
	bars.LineStyle.Width = vg.Points(0.5)

	// color bars by relative bitrate (low = green, high = vermilion)
	for i := range shots {
		if shots[i].Bitrate < 1500 {
			bars.Color = seriesColors[2] // green
		} else {
			bars.Color = seriesColors[5] // vermilion
		}
	}

	p.Add(bars)
	p.NominalY(labels...)

	return renderChart(p, opts)
}

// SaveChart writes chart bytes to a file. Format is inferred from extension.
func SaveChart(data []byte, path string) error {
	return os.WriteFile(path, data, 0o644)
}

// places a label annotation at the resolution transition point on the chart.
func addCrossoverAnnotation(p *plot.Plot, co hull.Crossover, idx int) {
	// stagger labels vertically to avoid overlap at close bitrates
	yOffset := 3.0 + float64(idx%2)*5.0
	label := fmt.Sprintf("%s\u2192%s", co.From.Label(), co.To.Label())
	labels, err := plotter.NewLabels(plotter.XYLabels{
		XYs:    []plotter.XY{{X: co.Bitrate, Y: co.VMAF + yOffset}},
		Labels: []string{label},
	})
	if err == nil {
		labels.TextStyle[0].Font.Size = vg.Points(8)
		labels.TextStyle[0].Color = color.RGBA{R: 100, G: 100, B: 100, A: 255}
		p.Add(labels)
	}
}

func renderChart(p *plot.Plot, opts Opts) ([]byte, error) {
	w := vg.Length(opts.Width) * vg.Inch
	h := vg.Length(opts.Height) * vg.Inch

	format := opts.Format
	if format == "" {
		format = "png"
	}

	writer, err := p.WriterTo(w, h, format)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s writer: %w", format, err)
	}

	var buf bytes.Buffer
	if _, err := writer.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", format, err)
	}

	return buf.Bytes(), nil
}

func shortCodecName(codec string) string {
	switch codec {
	case "libx264":
		return "H.264"
	case "libx265":
		return "H.265"
	case "libsvtav1":
		return "AV1"
	case "libvpx-vp9":
		return "VP9"
	default:
		return codec
	}
}

func findVMAFRange(points []hull.Point) (minV, maxV float64) {
	if len(points) == 0 {
		return 0, 100
	}
	minV = points[0].VMAF
	maxV = points[0].VMAF
	for _, p := range points[1:] {
		if p.VMAF < minV {
			minV = p.VMAF
		}
		if p.VMAF > maxV {
			maxV = p.VMAF
		}
	}
	return
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// SavePNG is an alias for SaveChart for backward compatibility.
func SavePNG(data []byte, path string) error {
	return SaveChart(data, path)
}

// ensure imports are used
var (
	_ = plotutil.AddLinePoints
)
