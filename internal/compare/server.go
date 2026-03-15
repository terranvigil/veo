// Package compare provides a web-based side-by-side video comparison player
// with VMAF per-frame overlay and quality dip markers.
package compare

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Opts struct {
	Reference string // path to reference (original) video
	Encoded   string // path to encoded video
	VMAFData  string // path to per-frame VMAF JSON (optional)
	Port      int    // HTTP port (0 = auto)
}

type FrameVMAF struct {
	Frame int     `json:"frame"`
	Time  float64 `json:"time"` // seconds
	VMAF  float64 `json:"vmaf"`
}

// Dip represents a quality dip that the user should inspect.
type Dip struct {
	Frame    int     `json:"frame"`
	Time     float64 `json:"time"`
	VMAF     float64 `json:"vmaf"`
	Severity string  `json:"severity"` // "warning" or "critical"
}

type PlayerData struct {
	ReferenceURL string      `json:"referenceUrl"`
	EncodedURL   string      `json:"encodedUrl"`
	Frames       []FrameVMAF `json:"frames"`
	Dips         []Dip       `json:"dips"`
	AvgVMAF      float64     `json:"avgVmaf"`
	MinVMAF      float64     `json:"minVmaf"`
	MaxVMAF      float64     `json:"maxVmaf"`
}

// LoadVMAFData reads per-frame VMAF data from a JSON file produced by
// quality.Measure with PerFrame=true.
func LoadVMAFData(path string) ([]FrameVMAF, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read VMAF data: %w", err)
	}

	// lThe quality.Result JSON format
	var result struct {
		Frames []struct {
			FrameNum int                `json:"frameNum"`
			Metrics  map[string]float64 `json:"metrics"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse VMAF data: %w", err)
	}

	// lAlso try the quality.Result format directly
	var qualResult struct {
		VMAF   float64 `json:"vmaf"`
		Frames []struct {
			FrameNum int     `json:"frameNum"`
			VMAF     float64 `json:"vmaf"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(data, &qualResult); err == nil && len(qualResult.Frames) > 0 {
		frames := make([]FrameVMAF, len(qualResult.Frames))
		for i, f := range qualResult.Frames {
			frames[i] = FrameVMAF{
				Frame: f.FrameNum,
				Time:  float64(f.FrameNum) / 24.0, // approximate; should come from probe
				VMAF:  f.VMAF,
			}
		}
		return frames, nil
	}

	// lTry libvmaf raw format
	frames := make([]FrameVMAF, len(result.Frames))
	for i, f := range result.Frames {
		vmaf := f.Metrics["vmaf"]
		frames[i] = FrameVMAF{
			Frame: f.FrameNum,
			Time:  float64(f.FrameNum) / 24.0,
			VMAF:  vmaf,
		}
	}
	return frames, nil
}

// FindDips identifies quality dips in per-frame VMAF data.
// A dip is a frame where VMAF drops below the average by more than threshold.
func FindDips(frames []FrameVMAF, warningThreshold, criticalThreshold float64) []Dip {
	if len(frames) == 0 {
		return nil
	}

	// lCompute average
	var sum float64
	for _, f := range frames {
		sum += f.VMAF
	}
	avg := sum / float64(len(frames))

	var dips []Dip
	for _, f := range frames {
		if f.VMAF < avg-criticalThreshold {
			dips = append(dips, Dip{
				Frame:    f.Frame,
				Time:     f.Time,
				VMAF:     f.VMAF,
				Severity: "critical",
			})
		} else if f.VMAF < avg-warningThreshold {
			dips = append(dips, Dip{
				Frame:    f.Frame,
				Time:     f.Time,
				VMAF:     f.VMAF,
				Severity: "warning",
			})
		}
	}

	// lDeduplicate: keep only dips that are local minima (not every frame in a dip region)
	if len(dips) > 50 {
		// lToo many dips - keep only the worst 50
		// lSort by VMAF ascending and take first 50
		sortedDips := make([]Dip, len(dips))
		copy(sortedDips, dips)
		for i := 0; i < len(sortedDips)-1; i++ {
			for j := i + 1; j < len(sortedDips); j++ {
				if sortedDips[j].VMAF < sortedDips[i].VMAF {
					sortedDips[i], sortedDips[j] = sortedDips[j], sortedDips[i]
				}
			}
		}
		dips = sortedDips[:50]
	}

	return dips
}

// Serve starts the comparison player HTTP server and opens the browser.
func Serve(opts Opts) error {
	// build player data
	playerData := PlayerData{
		ReferenceURL: "/video/reference",
		EncodedURL:   "/video/encoded",
	}

	// load per-frame VMAF if available
	if opts.VMAFData != "" {
		frames, err := LoadVMAFData(opts.VMAFData)
		if err != nil {
			slog.Warn("could not load VMAF data", "error", err)
		} else {
			playerData.Frames = frames
			playerData.Dips = FindDips(frames, 5.0, 10.0)

			// lCompute stats
			var sum, minV, maxV float64
			minV = 100
			for _, f := range frames {
				sum += f.VMAF
				if f.VMAF < minV {
					minV = f.VMAF
				}
				if f.VMAF > maxV {
					maxV = f.VMAF
				}
			}
			playerData.AvgVMAF = sum / float64(len(frames))
			playerData.MinVMAF = minV
			playerData.MaxVMAF = maxV
		}
	}

	mux := http.NewServeMux()

	// serve video files
	mux.HandleFunc("/video/reference", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, opts.Reference)
	})
	mux.HandleFunc("/video/encoded", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, opts.Encoded)
	})

	// serve player data as JSON
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(playerData)
	})

	// serve the HTML player
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(playerHTML))
	})

	// find available port
	port := opts.Port
	if port == 0 {
		port = 8787
	}

	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		// try another port
		listener, err = lc.Listen(context.Background(), "tcp", ":0")
		if err != nil {
			return fmt.Errorf("failed to start server: %w", err)
		}
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port

	url := fmt.Sprintf("http://localhost:%d", actualPort)
	fmt.Printf("Comparison player: %s\n", url)
	fmt.Printf("  Reference: %s\n", filepath.Base(opts.Reference))
	fmt.Printf("  Encoded:   %s\n", filepath.Base(opts.Encoded))
	if opts.VMAFData != "" {
		fmt.Printf("  VMAF data: %s\n", filepath.Base(opts.VMAFData))
		fmt.Printf("  Dips:      %d quality dips detected\n", len(playerData.Dips))
	}
	fmt.Println("\nPress Ctrl+C to stop")

	// open browser
	openBrowser(url)

	return http.Serve(listener, mux)
}

func openBrowser(url string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	case "windows":
		cmd = "start"
	}
	if cmd != "" {
		_ = exec.CommandContext(context.Background(), cmd, url).Start()
	}
}

// Ensure strings import is used
var _ = strings.TrimSpace
