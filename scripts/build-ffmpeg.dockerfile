# Build static FFmpeg + FFprobe with all codecs and filters needed by VEO.
#
# Produces Linux binaries for cloud/CI deployment.
# For macOS native binaries, use build-ffmpeg-macos.sh instead.
#
# Single arch (matches your Docker host):
#   docker build -f scripts/build-ffmpeg.dockerfile -o ./bin/ffmpeg .
#
# Multi-arch (for cloud deployment):
#   docker buildx build -f scripts/build-ffmpeg.dockerfile \
#     --platform linux/amd64,linux/arm64 \
#     -o type=local,dest=./bin/ffmpeg .
#
# Included codecs/filters:
#   x264, x265, SVT-AV1, dav1d, libvpx/VP9, libvmaf, opus

FROM debian:bookworm-slim AS build

ARG DEBIAN_FRONTEND=noninteractive

# Build tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    autoconf automake build-essential cmake git-core \
    libtool meson nasm ninja-build pkg-config wget yasm \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# ── x264 ──────────────────────────────────────────────────────────
RUN git clone --depth 1 https://github.com/mirror/x264.git && \
    cd x264 && \
    ./configure --prefix=/usr/local --enable-static --enable-pic --disable-cli && \
    make -j$(nproc) && make install

# ── x265 ──────────────────────────────────────────────────────────
RUN git clone --depth 1 -b 4.1 https://bitbucket.org/multicoreware/x265_git.git x265 && \
    cd x265/build/linux && \
    cmake -G "Unix Makefiles" \
        -DCMAKE_INSTALL_PREFIX=/usr/local \
        -DENABLE_SHARED=OFF \
        -DENABLE_CLI=OFF \
        -DSTATIC_LINK_CRT=ON \
        ../../source && \
    make -j$(nproc) && make install

# ── SVT-AV1 ──────────────────────────────────────────────────────
RUN git clone --depth 1 -b v4.0.0 https://gitlab.com/AOMediaCodec/SVT-AV1.git && \
    cd SVT-AV1/Build && \
    cmake -G "Unix Makefiles" \
        -DCMAKE_INSTALL_PREFIX=/usr/local \
        -DBUILD_SHARED_LIBS=OFF \
        -DBUILD_APPS=OFF \
        -DBUILD_DEC=ON \
        .. && \
    make -j$(nproc) && make install

# ── dav1d (AV1 decoder) ──────────────────────────────────────────
RUN git clone --depth 1 -b 1.5.0 https://github.com/videolan/dav1d.git && \
    cd dav1d && \
    meson setup build --prefix=/usr/local --default-library=static --buildtype=release && \
    ninja -C build && ninja -C build install

# ── libvpx (VP9) ─────────────────────────────────────────────────
RUN git clone --depth 1 -b v1.15.0 https://chromium.googlesource.com/webm/libvpx.git && \
    cd libvpx && \
    ./configure --prefix=/usr/local \
        --enable-static --disable-shared \
        --enable-vp9-highbitdepth \
        --disable-examples --disable-unit-tests --disable-docs && \
    make -j$(nproc) && make install

# ── libvmaf ───────────────────────────────────────────────────────
# Built as static lib; FFmpeg links against it via --enable-libvmaf.
# libvmaf 3.0 embeds VMAF models in the binary — no external model files needed.
RUN git clone --depth 1 -b v3.0.0 https://github.com/Netflix/vmaf.git && \
    cd vmaf/libvmaf && \
    meson setup build --prefix=/usr/local --default-library=static --buildtype=release && \
    ninja -C build && ninja -C build install

# ── opus (audio) ──────────────────────────────────────────────────
RUN git clone --depth 1 -b v1.5.2 https://github.com/xiph/opus.git && \
    cd opus && \
    autoreconf -fis && \
    ./configure --prefix=/usr/local --enable-static --disable-shared --disable-doc --disable-extra-programs && \
    make -j$(nproc) && make install

# ── FFmpeg ────────────────────────────────────────────────────────
RUN git clone --depth 1 -b n8.0.1 https://github.com/FFmpeg/FFmpeg.git ffmpeg-src && \
    cd ffmpeg-src && \
    wget -qO- "https://git.ffmpeg.org/gitweb/ffmpeg.git/patch/a5d4c398b411a00ac09d8fe3b66117222323844c" | git apply || true && \
    PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/local/lib/aarch64-linux-gnu/pkgconfig" \
    ./configure \
        --prefix=/usr/local \
        --enable-gpl \
        --enable-version3 \
        --enable-static \
        --disable-shared \
        --enable-libx264 \
        --enable-libx265 \
        --enable-libsvtav1 \
        --enable-libdav1d \
        --enable-libvpx \
        --enable-libvmaf \
        --enable-libopus \
        --disable-doc \
        --disable-htmlpages \
        --disable-manpages \
        --disable-podpages \
        --disable-txtpages \
        --extra-cflags="-I/usr/local/include" \
        --extra-ldflags="-L/usr/local/lib -L/usr/local/lib/x86_64-linux-gnu -L/usr/local/lib/aarch64-linux-gnu" \
        --extra-libs="-lpthread -lm -lstdc++" \
        --pkg-config-flags="--static" && \
    make -j$(nproc) && make install

# Verify the build
RUN ffmpeg -version && \
    ffmpeg -encoders 2>/dev/null | grep -E "libx264|libx265|libsvtav1" && \
    ffmpeg -filters 2>/dev/null | grep libvmaf && \
    ffprobe -version

# ── Output stage ──────────────────────────────────────────────────
FROM scratch AS export
COPY --from=build /usr/local/bin/ffmpeg /ffmpeg
COPY --from=build /usr/local/bin/ffprobe /ffprobe
