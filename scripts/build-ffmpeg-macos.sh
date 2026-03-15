#!/usr/bin/env bash
#
# build-ffmpeg-macos.sh — Build FFmpeg from source on macOS with libvmaf.
#
# Produces native macOS arm64 binaries at bin/ffmpeg/ with:
#   x264, x265, SVT-AV1, dav1d, libvpx/VP9, libvmaf, opus
#   + VideoToolbox and AudioToolbox hardware acceleration
#
# Requirements:
#   - Homebrew (arm64, /opt/homebrew)
#   - Xcode Command Line Tools
#
# Usage:
#   ./scripts/build-ffmpeg-macos.sh
#
# Output:
#   bin/ffmpeg/ffmpeg
#   bin/ffmpeg/ffprobe

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/bin/ffmpeg"
BUILD_DIR="$PROJECT_ROOT/.ffmpeg-build"
FFMPEG_VERSION="n8.0.1"

BREW="${HOMEBREW_PREFIX:-/opt/homebrew}/bin/brew"

if [[ ! -x "$BREW" ]]; then
    echo "ERROR: Homebrew not found at $BREW"
    echo "Install arm64 Homebrew: https://brew.sh"
    exit 1
fi

echo "==> Installing dependencies via Homebrew..."
$BREW install --quiet \
    nasm pkg-config \
    x264 x265 svt-av1 dav1d libvpx libvmaf opus

BREW_PREFIX="$($BREW --prefix)"

echo ""
echo "==> Cloning FFmpeg $FFMPEG_VERSION..."
mkdir -p "$BUILD_DIR"
if [[ ! -d "$BUILD_DIR/ffmpeg-src" ]]; then
    git clone --depth 1 -b "$FFMPEG_VERSION" \
        https://github.com/FFmpeg/FFmpeg.git "$BUILD_DIR/ffmpeg-src"
else
    echo "    (already cloned)"
fi

cd "$BUILD_DIR/ffmpeg-src"

# Clean previous build if any
make distclean 2>/dev/null || true

# Apply SVT-AV1 4.0 compatibility patch (aq_mode rename)
echo "==> Applying SVT-AV1 4.0 compatibility patch..."
curl -sL "https://git.ffmpeg.org/gitweb/ffmpeg.git/patch/a5d4c398b411a00ac09d8fe3b66117222323844c" | git apply --check 2>/dev/null && \
    curl -sL "https://git.ffmpeg.org/gitweb/ffmpeg.git/patch/a5d4c398b411a00ac09d8fe3b66117222323844c" | git apply || \
    echo "    (patch already applied or not needed)"

echo ""
echo "==> Configuring FFmpeg..."
PKG_CONFIG_PATH="$BREW_PREFIX/lib/pkgconfig" \
./configure \
    --prefix="$BUILD_DIR/install" \
    --enable-gpl \
    --enable-version3 \
    --enable-nonfree \
    --enable-libx264 \
    --enable-libx265 \
    --enable-libsvtav1 \
    --enable-libdav1d \
    --enable-libvpx \
    --enable-libvmaf \
    --enable-libopus \
    --enable-videotoolbox \
    --enable-audiotoolbox \
    --enable-neon \
    --disable-doc \
    --disable-htmlpages \
    --disable-manpages \
    --disable-podpages \
    --disable-txtpages \
    --extra-cflags="-I$BREW_PREFIX/include" \
    --extra-ldflags="-L$BREW_PREFIX/lib"

echo ""
echo "==> Building FFmpeg (this takes 5-10 minutes)..."
make -j$(sysctl -n hw.ncpu)
make install

echo ""
echo "==> Copying binaries to $OUTPUT_DIR..."
mkdir -p "$OUTPUT_DIR"
cp "$BUILD_DIR/install/bin/ffmpeg" "$OUTPUT_DIR/ffmpeg"
cp "$BUILD_DIR/install/bin/ffprobe" "$OUTPUT_DIR/ffprobe"

echo ""
echo "==> Verifying..."
file "$OUTPUT_DIR/ffmpeg"
echo ""
"$OUTPUT_DIR/ffmpeg" -version 2>&1 | head -1
echo ""
echo "Encoders:"
"$OUTPUT_DIR/ffmpeg" -encoders 2>/dev/null | grep -E "libx264|libx265|libsvtav1|libvpx"
echo ""
echo "VMAF filter:"
"$OUTPUT_DIR/ffmpeg" -filters 2>/dev/null | grep libvmaf
echo ""
echo "==> Done. VEO will auto-discover binaries at bin/ffmpeg/"
echo "    Or set: export VEO_FFMPEG=$OUTPUT_DIR/ffmpeg"
