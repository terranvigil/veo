package ffmpeg

import (
	"os"
	"path/filepath"
	"runtime"
)

// FFmpegPath returns the path to the ffmpeg binary.
// Resolution order:
//  1. VEO_FFMPEG environment variable
//  2. bin/ffmpeg/ffmpeg relative to the working directory (Docker-built)
//  3. "ffmpeg" (system PATH)
func FFmpegPath() string {
	if p := os.Getenv("VEO_FFMPEG"); p != "" {
		return p
	}
	if p := localBinary("ffmpeg"); p != "" {
		return p
	}
	return "ffmpeg"
}

// FFprobePath returns the path to the ffprobe binary.
// Resolution order:
//  1. VEO_FFPROBE environment variable
//  2. bin/ffmpeg/ffprobe relative to the working directory (Docker-built)
//  3. "ffprobe" (system PATH)
func FFprobePath() string {
	if p := os.Getenv("VEO_FFPROBE"); p != "" {
		return p
	}
	if p := localBinary("ffprobe"); p != "" {
		return p
	}
	return "ffprobe"
}

func localBinary(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join("bin", "ffmpeg", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}
