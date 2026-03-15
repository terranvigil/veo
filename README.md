# VEO - Video Encoding Optimizer

VEO analyzes video content and computes optimal encoding parameters using
perceptual quality measurement (VMAF) and convex hull analysis. Instead of
applying a one-size-fits-all bitrate ladder, VEO tailors encoding decisions
to the content, producing better quality at lower bitrates.

**Acknowledgment:** VEO builds on decades of research in rate-distortion theory,
perceptual quality measurement, and content-adaptive streaming. Thank you to the
engineers and researchers at Netflix, Beamr, Fraunhofer, Mux, and the broader
video encoding community whose published work, open-source tools, and
foundational science inform every part of this project.

## Optimization Methods

VEO provides four encoding optimization methods, each suited to different
use cases and levels of granularity:

| Method | Granularity | Best For | Description |
|--------|-------------|----------|-------------|
| [Per-Title](docs/per-title-encoding.md) | Whole video | VOD catalogs | Computes a custom bitrate ladder per video using convex hull analysis across resolutions, codecs, and quality levels |
| [Per-Shot](docs/per-shot-encoding.md) | Shot (2-30s) | Feature films, episodic | Detects scene boundaries and allocates bits across shots using Trellis optimization - complex scenes get more bits, simple scenes get fewer |
| [Segment-Level CRF](docs/segment-level-adaptation.md) | 1-second segments | Variable complexity content | Adapts CRF per temporal segment with closed-loop VMAF verification to maintain consistent quality |
| [Context-Aware](docs/content-adaptive-encoding.md) | Per device class | Multi-device streaming | Generates device-specific ladders (mobile/desktop/TV) with appropriate resolution caps, codecs, and VMAF models |

All methods can be combined with the [comparison player](docs/comparison-player.md)
for visual QA of results.

## Table of Contents

- [Quick Start](#quick-start)
- [Usage](#usage)
- [Documentation](#documentation)
- [Supported Codecs](#supported-codecs)
- [Project Structure](#project-structure)
- [Status](#status)
- [License](#license)

## Quick Start

```bash
# Build VEO
make build

# Build FFmpeg with libvmaf + SVT-AV1 (macOS)
./scripts/build-ffmpeg.sh

# Download test assets
./scripts/download-assets.sh --tier micro

# Run your first per-title analysis
./veo per-title analyze -i assets/sd/akiyo_cif.y4m \
  --resolutions 240p --codecs libx264 --preset ultrafast
```

See [prerequisites](#prerequisites) for detailed setup.

## Usage

### Per-title analysis

```bash
./veo per-title analyze -i video.y4m \
  --codecs libx264,libsvtav1 \
  --resolutions 480p,720p,1080p \
  --parallel 4 --charts ./charts -o results.json
```

Options: `--mode crf|qp`, `--dry-run`, `--checkpoint file.json`, `--encode-output dir`

### Per-shot analysis

```bash
./veo per-shot detect -i video.y4m --threshold 10
./veo per-shot analyze -i video.y4m --target-bitrate 2000
```

### Segment-level CRF adaptation

```bash
./veo per-segment analyze -i video.y4m --target-vmaf 93 --codec libx264
```

### Context-aware encoding

```bash
./veo context-aware analyze -i video.y4m --devices mobile,desktop,tv
```

### Visual QA with comparison player

```bash
veo quality measure --reference original.mp4 --distorted encoded.mp4 \
  --per-frame -o vmaf_data.json
veo compare --reference original.mp4 --encoded encoded.mp4 \
  --vmaf-data vmaf_data.json
```

### Other commands

```bash
./veo inspect probe video.mp4              # Show video metadata
./veo encode input.y4m -o out.mp4          # Encode a video
./veo quality measure --reference a --distorted b  # Measure VMAF/PSNR/SSIM
```

## Documentation

| Document | Description |
|----------|-------------|
| [Per-Title Encoding](docs/per-title-encoding.md) | Convex hull analysis, R-D optimization, ladder selection |
| [Per-Shot Encoding](docs/per-shot-encoding.md) | Shot detection, Trellis optimization, constant-slope bit allocation |
| [Content-Adaptive Encoding](docs/content-adaptive-encoding.md) | Device profiles, multi-codec hulls, ML prediction concepts |
| [Segment-Level CRF Adaptation](docs/segment-level-adaptation.md) | Segment-level CRF tuning with complexity analysis |
| [Quality Metrics](docs/quality-metrics.md) | VMAF, PSNR, SSIM, SSIMULACRA2, BD-Rate |
| [Rate Control](docs/rate-control.md) | CRF vs QP vs VBR - which mode and when |
| [Shot Detection](docs/shot-detection.md) | FFmpeg scdet, PySceneDetect, TransNetV2 - comparison and guidelines |
| [Chunked Encoding](docs/chunked-encoding.md) | Parallel encoding with shot-aware chunking for production workflows |
| [Comparison Player](docs/comparison-player.md) | Side-by-side visual QA with VMAF timeline and quality dip markers |

## Supported Codecs

| Codec | Flag | Notes |
|-------|------|-------|
| H.264/AVC | `libx264` | Fastest encode, widest device support |
| H.265/HEVC | `libx265` | ~30-40% better compression than H.264 |
| AV1 | `libsvtav1` | ~50% better compression, royalty-free, SVT-AV1 4.0 |

## Prerequisites

- **Go 1.22+** - `brew install go`
- **FFmpeg with libvmaf** - `./scripts/build-ffmpeg.sh` (macOS) or `./scripts/build-ffmpeg.sh --docker` (Linux)
- **Docker** - for Linux/cloud FFmpeg builds only

FFmpeg 8.0.1 is built with: x264, x265, SVT-AV1 4.0, dav1d, VP9, libvmaf 3.0,
opus, and VideoToolbox (macOS). Binaries go to `bin/ffmpeg/` and VEO auto-discovers
them, or set `VEO_FFMPEG` / `VEO_FFPROBE` explicitly.

### Test assets

```bash
./scripts/download-assets.sh --tier micro    # ~130 MB - 3 SD clips
./scripts/download-assets.sh --tier small    # ~2 GB  - + AWCY benchmark set
./scripts/download-assets.sh --tier medium   # ~10 GB - + HD clips + Netflix 4K
./scripts/download-assets.sh --list          # see all tiers and clips
```

Sourced from Xiph/Derf, Netflix Open Content (CC-BY 4.0), UVG, AWCY, and Blender.

## Project Structure

```
veo/
├── cmd/veo/              CLI (Cobra commands)
├── internal/
│   ├── ffmpeg/           FFmpeg/FFprobe wrapper
│   ├── quality/          VMAF/PSNR/SSIM measurement
│   ├── encoding/         Shared config and preset mapping
│   ├── hull/             Convex hull + BD-Rate
│   ├── ladder/           Ladder selection with crossover enforcement
│   ├── shot/             Shot detection (scdet)
│   ├── complexity/       Spatial/temporal/DCT complexity analysis
│   ├── pertitle/         Per-title analysis pipeline
│   ├── pershot/          Per-shot + Trellis optimization
│   ├── persegment/       Segment-level CRF adaptation
│   ├── contextaware/     Device-specific ladder generation
│   ├── checkpoint/       Resume support for long analyses
│   ├── compare/          Browser-based comparison player
│   └── chart/            PNG/SVG chart generation
├── scripts/              FFmpeg build + asset download
├── docs/                 Principles and science docs
└── assets/               Downloaded test videos (gitignored)
```

## Status

All four optimization methods implemented and validated:

**Per-Title** - Convex hull, BD-Rate (-49.7% AV1 vs x264), resolution crossover
enforcement, Netflix fixed ladder comparison, CRF and QP trial modes,
final encode output, checkpointing, PNG chart generation.

**Per-Shot** - Shot detection (scdet), per-shot hulls, Trellis Lagrangian
bit allocation. Validated on Sintel trailer (12x efficiency variation). Analysis only - chunked
encoding for final output is [documented](docs/chunked-encoding.md) for future implementation.

**Segment-Level CRF** - Complexity analysis (entropy + YDIF + DCT energy),
binary-search CRF per 1-second segment, closed-loop VMAF verification.
This is segment-level adaptation, not true per-frame like Beamr CABR.

**Context-Aware** - Device profiles (mobile/desktop/TV/4K TV) with resolution
caps, codec preferences, and VMAF model selection. Parameterized per-title
analysis - does not include network-aware adaptation.

**Infrastructure** - 55+ tests, golangci-lint, GitHub Actions CI, structured
logging, checkpointing, comparison player with VMAF dip detection.

### Backlog
- Chunked encoding ([documented](docs/chunked-encoding.md))
- ML feature extraction and prediction
- REST API

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

H.264/HEVC encoding may require patent licenses depending on use case.
AV1 is royalty-free. See [NOTICE](NOTICE) for third-party attributions.
