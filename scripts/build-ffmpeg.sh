#!/usr/bin/env bash
#
# build-ffmpeg.sh — Build FFmpeg for the current platform.
#
# On macOS: builds native arm64 binaries from source using Homebrew deps.
# On Linux / CI: builds static binaries via Docker.
#
# Usage:
#   ./scripts/build-ffmpeg.sh                          # auto-detect platform
#   ./scripts/build-ffmpeg.sh --docker                 # force Docker (Linux)
#   ./scripts/build-ffmpeg.sh --docker --multi         # Docker: amd64 + arm64
#   ./scripts/build-ffmpeg.sh --macos                  # force macOS native
#
# Output:
#   bin/ffmpeg/ffmpeg
#   bin/ffmpeg/ffprobe

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE=""
MULTI=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --docker) MODE="docker"; shift ;;
        --macos)  MODE="macos"; shift ;;
        --multi)  MULTI=true; shift ;;
        --help|-h)
            sed -n '3,15p' "$0" | sed 's/^# \?//'
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# Auto-detect platform
if [[ -z "$MODE" ]]; then
    if [[ "$(uname -s)" == "Darwin" ]]; then
        MODE="macos"
    else
        MODE="docker"
    fi
fi

if [[ "$MODE" == "macos" ]]; then
    exec "$SCRIPT_DIR/build-ffmpeg-macos.sh"
fi

# ── Docker build (Linux) ─────────────────────────────────────────

if ! command -v docker &>/dev/null; then
    echo "ERROR: Docker is required for Linux builds."
    exit 1
fi

PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_BASE="$PROJECT_ROOT/bin/ffmpeg"

build_platform() {
    local platform="$1"
    local output_dir="$OUTPUT_BASE"

    # For multi-arch, separate into subdirs
    if $MULTI; then
        output_dir="$OUTPUT_BASE/${platform#linux/}"
    fi

    echo "==> Building FFmpeg for $platform via Docker..."
    echo "==> Output: $output_dir/"
    echo ""

    mkdir -p "$output_dir"

    docker buildx build \
        -f "$SCRIPT_DIR/build-ffmpeg.dockerfile" \
        --platform "$platform" \
        -o "type=local,dest=$output_dir" \
        "$PROJECT_ROOT"

    echo ""
    echo "==> Build complete for $platform:"
    file "$output_dir/ffmpeg"
    file "$output_dir/ffprobe"
    echo ""
}

if $MULTI; then
    build_platform "linux/amd64"
    build_platform "linux/arm64"
else
    # Default to host architecture
    host_arch=$(docker info --format '{{.Architecture}}' 2>/dev/null || echo "aarch64")
    case "$host_arch" in
        x86_64)  build_platform "linux/amd64" ;;
        aarch64) build_platform "linux/arm64" ;;
        *)       build_platform "linux/$host_arch" ;;
    esac
fi

echo "==> Done."
