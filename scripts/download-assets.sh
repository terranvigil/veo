#!/usr/bin/env bash
#
# download-assets.sh — Download test video assets for VEO encoding optimization.
#
# Usage:
#   ./scripts/download-assets.sh [--tier <tier>] [--clip <name>] [--list] [--dry-run]
#
# Tiers:
#   micro   ~200 MB   3 short SD clips from Xiph (fastest, for unit tests)
#   small   ~2 GB     AWCY objective-1-fast (standard codec benchmark set)
#   medium  ~10 GB    Small + select Xiph HD + 1 Netflix scene
#   large   ~35 GB    Medium + UVG 4K subset + more Netflix scenes
#   full    ~65 GB    Everything (UVG full + Netflix Chimera + AWCY + Xiph HD)
#
# Examples:
#   ./scripts/download-assets.sh --tier micro
#   ./scripts/download-assets.sh --tier small
#   ./scripts/download-assets.sh --clip crowd_run_1080p50
#   ./scripts/download-assets.sh --list
#   ./scripts/download-assets.sh --tier medium --dry-run

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ASSETS_DIR="$PROJECT_ROOT/assets"

XIPH_BASE="https://media.xiph.org/video/derf"
XIPH_S3="s3://xiph-media/video/derf"
AWCY_BASE="https://media.xiph.org/sets"
NETFLIX_S3="s3://download.opencontent.netflix.com"
UVG_BASE="https://ultravideo.fi/video"

TIER=""
CLIP=""
DRY_RUN=false
LIST=false
USE_S3=false

# Check if AWS CLI is available for faster S3 downloads
if command -v aws &>/dev/null; then
    USE_S3=true
fi

usage() {
    sed -n '3,16p' "$0" | sed 's/^# \?//'
    exit 0
}

log() {
    echo "==> $*"
}

warn() {
    echo "WARNING: $*" >&2
}

die() {
    echo "ERROR: $*" >&2
    exit 1
}

# Download a file with resume support
# Usage: download <url> <output_path> [expected_sha256]
download() {
    local url="$1"
    local output="$2"
    local sha256="${3:-}"

    if [[ -f "$output" ]] && [[ -n "$sha256" ]]; then
        local actual
        actual=$(shasum -a 256 "$output" 2>/dev/null | awk '{print $1}')
        if [[ "$actual" == "$sha256" ]]; then
            log "Already downloaded: $(basename "$output")"
            return 0
        fi
    elif [[ -f "$output" ]]; then
        log "Already exists (no checksum): $(basename "$output")"
        return 0
    fi

    mkdir -p "$(dirname "$output")"

    if $DRY_RUN; then
        log "[dry-run] Would download: $url"
        log "[dry-run]            to: $output"
        return 0
    fi

    log "Downloading: $(basename "$output")"
    log "  From: $url"

    if curl -fSL -C - -o "$output" "$url"; then
        if [[ -n "$sha256" ]]; then
            local actual
            actual=$(shasum -a 256 "$output" | awk '{print $1}')
            if [[ "$actual" != "$sha256" ]]; then
                warn "Checksum mismatch for $(basename "$output")"
                warn "  Expected: $sha256"
                warn "  Got:      $actual"
                return 1
            fi
        fi
        log "  Done: $(du -h "$output" | awk '{print $1}')"
    else
        warn "Failed to download: $url"
        return 1
    fi
}

# Download from S3 (faster, supports resume)
# Usage: s3_download <s3_path> <output_path>
s3_download() {
    local s3_path="$1"
    local output="$2"

    if [[ -f "$output" ]]; then
        log "Already exists: $(basename "$output")"
        return 0
    fi

    mkdir -p "$(dirname "$output")"

    if $DRY_RUN; then
        log "[dry-run] Would download: $s3_path"
        log "[dry-run]            to: $output"
        return 0
    fi

    log "Downloading (S3): $(basename "$output")"
    if aws s3 cp --no-sign-request "$s3_path" "$output"; then
        log "  Done: $(du -h "$output" | awk '{print $1}')"
    else
        warn "S3 download failed, falling back to HTTP"
        return 1
    fi
}

# Download uncompressed y4m from Xiph
# Usage: download_xiph <remote_filename> <subdir> [local_filename]
# Most Xiph SD/HD clips are uncompressed .y4m files
download_xiph() {
    local remote_name="$1"
    local subdir="$2"
    local local_name="${3:-$remote_name}"
    local output_dir="$ASSETS_DIR/$subdir"
    local y4m_path="$output_dir/${local_name}.y4m"

    if [[ -f "$y4m_path" ]]; then
        log "Already exists: $subdir/${local_name}.y4m"
        return 0
    fi

    # Try S3 first, then HTTP
    if $USE_S3; then
        s3_download "$XIPH_S3/y4m/${remote_name}.y4m" "$y4m_path" || \
            download "$XIPH_BASE/y4m/${remote_name}.y4m" "$y4m_path"
    else
        download "$XIPH_BASE/y4m/${remote_name}.y4m" "$y4m_path"
    fi
}

# Download and decompress xz-compressed y4m from Xiph
# Usage: download_xiph_xz <name> <subdir>
# Only full-length films (Big Buck Bunny, Sintel, etc.) are xz-compressed
download_xiph_xz() {
    local name="$1"
    local subdir="$2"
    local output_dir="$ASSETS_DIR/$subdir"
    local y4m_path="$output_dir/${name}.y4m"
    local xz_path="$output_dir/${name}.y4m.xz"

    # Already decompressed
    if [[ -f "$y4m_path" ]]; then
        log "Already exists: $subdir/${name}.y4m"
        return 0
    fi

    # Try S3 first, then HTTP
    if $USE_S3; then
        s3_download "$XIPH_S3/y4m/${name}.y4m.xz" "$xz_path" || \
            download "$XIPH_BASE/y4m/${name}.y4m.xz" "$xz_path"
    else
        download "$XIPH_BASE/y4m/${name}.y4m.xz" "$xz_path"
    fi

    # Decompress
    if [[ -f "$xz_path" ]] && ! $DRY_RUN; then
        log "Decompressing: ${name}.y4m.xz"
        xz -dk "$xz_path" 2>/dev/null || unxz -k "$xz_path" 2>/dev/null || {
            warn "Failed to decompress ${name}.y4m.xz — is xz installed?"
            return 1
        }
        # Remove compressed version to save space
        rm -f "$xz_path"
        log "  Decompressed: $(du -h "$y4m_path" | awk '{print $1}')"
    fi
}

# Download AWCY pre-packaged set
download_awcy() {
    local set_name="$1"
    local output_dir="$ASSETS_DIR/awcy"

    if [[ -d "$output_dir/$set_name" ]]; then
        log "Already exists: awcy/$set_name/"
        return 0
    fi

    local tarball="$output_dir/${set_name}.tar.gz"
    download "$AWCY_BASE/${set_name}.tar.gz" "$tarball"

    if [[ -f "$tarball" ]] && ! $DRY_RUN; then
        log "Extracting: ${set_name}.tar.gz"
        mkdir -p "$output_dir/$set_name"
        tar xzf "$tarball" -C "$output_dir/$set_name"
        rm -f "$tarball"
        log "  Extracted to: awcy/$set_name/"
    fi
}

# Download Netflix Open Content scene
# Usage: download_netflix <scene_name> <format_path>
download_netflix() {
    local scene_name="$1"
    local s3_path="$2"
    local output_dir="$ASSETS_DIR/netflix"
    local output_path="$output_dir/$scene_name"

    if [[ -d "$output_path" ]] || [[ -f "$output_path" ]]; then
        log "Already exists: netflix/$scene_name"
        return 0
    fi

    mkdir -p "$output_dir"

    if $DRY_RUN; then
        log "[dry-run] Would download Netflix: $scene_name"
        return 0
    fi

    if $USE_S3; then
        log "Downloading Netflix: $scene_name"
        aws s3 cp --no-sign-request --recursive \
            "$NETFLIX_S3/$s3_path" "$output_path" || {
            warn "Failed to download Netflix $scene_name (requires AWS CLI)"
            return 1
        }
    else
        warn "Netflix content requires AWS CLI. Install it with: brew install awscli"
        return 1
    fi
}

# Download UVG 4K sequence (raw YUV)
download_uvg() {
    local name="$1"
    local fps="$2"
    local output_dir="$ASSETS_DIR/uvg"
    local filename="${name}_3840x2160_${fps}fps_420_8bit.yuv"
    local output_path="$output_dir/$filename"

    if [[ -f "$output_path" ]]; then
        log "Already exists: uvg/$filename"
        return 0
    fi

    # UVG provides 7z archives
    local archive="${name}_3840x2160_${fps}fps_420_8bit.7z"
    download "$UVG_BASE/$archive" "$output_dir/$archive"

    if [[ -f "$output_dir/$archive" ]] && ! $DRY_RUN; then
        if command -v 7z &>/dev/null; then
            log "Extracting: $archive"
            7z x -o"$output_dir" "$output_dir/$archive"
            rm -f "$output_dir/$archive"
        else
            warn "7z not found. Install with: brew install p7zip"
            warn "Archive saved at: uvg/$archive"
        fi
    fi
}

# ── Tier definitions ──────────────────────────────────────────────

tier_micro() {
    log "Tier: micro (~130 MB) — 3 SD clips for unit tests"
    # SD clips: uncompressed y4m, ~44 MB each
    download_xiph "akiyo_cif" "sd"
    download_xiph "foreman_cif" "sd"
    download_xiph "mobile_cif" "sd"
}

tier_small() {
    log "Tier: small (~2 GB) — AWCY objective-1-fast benchmark set"
    tier_micro
    download_awcy "objective-1-fast"
}

tier_medium() {
    log "Tier: medium (~10 GB) — HD clips + 1 Netflix scene"
    tier_small

    # Xiph HD — core benchmark clips (uncompressed y4m)
    download_xiph "crowd_run_1080p50" "hd"           # 1.5 GB
    download_xiph "park_joy_1080p50" "hd"             # 1.5 GB
    download_xiph "riverbed_1080p25" "hd"             # 743 MB
    download_xiph "rush_hour_1080p25" "hd"            # 1.5 GB
    download_xiph "sunflower_1080p25" "hd"            # 1.5 GB

    # Xiph HD — talking head / videoconference (JVET Class E)
    # Note: Xiph uses _1280x720_60 naming for these
    download_xiph "Johnny_1280x720_60" "hd" "Johnny_720p60"
    download_xiph "KristenAndSara_1280x720_60" "hd" "KristenAndSara_720p60"
    download_xiph "FourPeople_1280x720_60" "hd" "FourPeople_720p60"

    # Netflix — one challenging scene
    download_netflix "Chimera_DinnerScene" \
        "Chimera/Chimera_DinnerScene_4096x2160_59.94_10bit_420_hdr_bt2020_pq"
}

tier_large() {
    log "Tier: large (~35 GB) — + UVG 4K subset + more Netflix"
    tier_medium

    # More Xiph HD (uncompressed y4m)
    download_xiph "blue_sky_1080p25" "hd"             # 645 MB
    download_xiph "pedestrian_area_1080p25" "hd"      # 1.1 GB
    download_xiph "station2_1080p25" "hd"             # 930 MB
    download_xiph "tractor_1080p25" "hd"              # 2.1 GB

    # Xiph 4K (uncompressed y4m)
    download_xiph "crowd_run_2160p50" "4k"            # 5.8 GB
    download_xiph "ducks_take_off_2160p50" "4k"       # 5.8 GB
    download_xiph "old_town_cross_2160p50" "4k"       # 5.8 GB

    # UVG 4K (select clips)
    download_uvg "Beauty" "120"
    download_uvg "Bosphorus" "120"
    download_uvg "Jockey" "120"
    download_uvg "ShakeNDry" "120"

    # Netflix — more scenes
    download_netflix "Chimera_BarScene" \
        "Chimera/Chimera_BarScene_4096x2160_59.94_10bit_420_hdr_bt2020_pq"
    download_netflix "Chimera_Aerial" \
        "Chimera/Chimera_Aerial_4096x2160_59.94_10bit_420_hdr_bt2020_pq"
    download_netflix "ElFuente_FoodMarket" \
        "ElFuente/ElFuente_FoodMarket_4096x2160_59.94_10bit_420"
}

tier_full() {
    log "Tier: full (~65 GB) — everything"
    tier_large

    # Remaining UVG 4K
    download_uvg "HoneyBee" "120"
    download_uvg "ReadySetGo" "120"
    download_uvg "YachtRide" "120"
    download_uvg "Lips" "120"

    # 50fps UVG
    download_uvg "CityAlley" "50"
    download_uvg "FlowerFocus" "50"
    download_uvg "FlowerPan" "50"
    download_uvg "RaceNight" "50"
    download_uvg "RiverBank" "50"
    download_uvg "Twilight" "50"

    # More Netflix
    download_netflix "ElFuente_Tango" \
        "ElFuente/ElFuente_Tango_4096x2160_59.94_10bit_420"
    download_netflix "ElFuente_BoxingPractice" \
        "ElFuente/ElFuente_BoxingPractice_4096x2160_59.94_10bit_420"
    download_netflix "Chimera_WindAndNature" \
        "Chimera/Chimera_WindAndNature_4096x2160_59.94_10bit_420_hdr_bt2020_pq"
    download_netflix "Chimera_RollerCoaster" \
        "Chimera/Chimera_RollerCoaster_4096x2160_59.94_10bit_420_hdr_bt2020_pq"

    # AWCY full set
    download_awcy "objective-1"

    # Blender films (xz-compressed y4m — these are large)
    download_xiph_xz "sintel_trailer_2k_480p24" "blender"     # 89 MB compressed
    download_xiph_xz "sintel_trailer_2k_1080p24" "blender"    # 368 MB compressed
    download_xiph_xz "big_buck_bunny_1080p24" "blender"       # 10 GB compressed, 42 GB uncompressed!
}

# ── Individual clip download ──────────────────────────────────────

download_clip() {
    local clip="$1"
    case "$clip" in
        # SD (uncompressed y4m)
        akiyo*) download_xiph "akiyo_cif" "sd" ;;
        foreman*) download_xiph "foreman_cif" "sd" ;;
        mobile*) download_xiph "mobile_cif" "sd" ;;
        stefan*) download_xiph "stefan_sif" "sd" ;;

        # HD (uncompressed y4m)
        crowd_run_1080*) download_xiph "crowd_run_1080p50" "hd" ;;
        crowd_run_2160*|crowd_run_4k*) download_xiph "crowd_run_2160p50" "4k" ;;
        park_joy*) download_xiph "park_joy_1080p50" "hd" ;;
        riverbed*) download_xiph "riverbed_1080p25" "hd" ;;
        rush_hour*) download_xiph "rush_hour_1080p25" "hd" ;;
        sunflower*) download_xiph "sunflower_1080p25" "hd" ;;
        blue_sky*) download_xiph "blue_sky_1080p25" "hd" ;;
        pedestrian*) download_xiph "pedestrian_area_1080p25" "hd" ;;
        station2*) download_xiph "station2_1080p25" "hd" ;;
        tractor*) download_xiph "tractor_1080p25" "hd" ;;

        # HD — videoconference (Xiph uses _1280x720_60 naming)
        johnny*|Johnny*) download_xiph "Johnny_1280x720_60" "hd" "Johnny_720p60" ;;
        kristen*|KristenAndSara*) download_xiph "KristenAndSara_1280x720_60" "hd" "KristenAndSara_720p60" ;;
        four_people*|FourPeople*) download_xiph "FourPeople_1280x720_60" "hd" "FourPeople_720p60" ;;

        # 4K Xiph (uncompressed y4m)
        ducks*) download_xiph "ducks_take_off_2160p50" "4k" ;;
        old_town*) download_xiph "old_town_cross_2160p50" "4k" ;;
        in_to_tree*) download_xiph "in_to_tree_2160p50" "4k" ;;

        # UVG
        beauty*|Beauty*) download_uvg "Beauty" "120" ;;
        bosphorus*|Bosphorus*) download_uvg "Bosphorus" "120" ;;
        jockey*|Jockey*) download_uvg "Jockey" "120" ;;
        shake*|ShakeNDry*) download_uvg "ShakeNDry" "120" ;;
        honeybee*|HoneyBee*) download_uvg "HoneyBee" "120" ;;

        # AWCY sets
        objective-1-fast) download_awcy "objective-1-fast" ;;
        objective-1) download_awcy "objective-1" ;;

        # Blender (xz-compressed y4m)
        big_buck_bunny*|bbb*) download_xiph_xz "big_buck_bunny_1080p24" "blender" ;;
        sintel*) download_xiph_xz "sintel_trailer_2k_1080p24" "blender" ;;

        *) die "Unknown clip: $clip. Use --list to see available clips." ;;
    esac
}

# ── List available clips ──────────────────────────────────────────

list_clips() {
    cat <<'CLIPS'
Tiers:
  micro   ~200 MB   3 SD clips (akiyo, foreman, mobile) — unit tests
  small   ~2 GB     micro + AWCY objective-1-fast benchmark set
  medium  ~10 GB    small + 8 Xiph HD clips + 1 Netflix 4K scene
  large   ~35 GB    medium + 4 Xiph 4K + 4 UVG 4K + 3 Netflix scenes
  full    ~65 GB    large + remaining UVG + Netflix + AWCY full + Blender

Individual clips (use with --clip):

  SD (Xiph/Derf, CIF):
    akiyo               Talking head, low motion
    foreman             Moderate motion, head + camera movement
    mobile              Scrolling calendar + toy train, high spatial complexity
    stefan              Tennis player, fast motion

  HD (Xiph, 1080p/720p):
    crowd_run_1080p50   Dense crowd, very challenging (50fps)
    park_joy            Park scene, complex motion (50fps)
    riverbed            Extremely high texture complexity (25fps)
    rush_hour           Traffic scene (25fps)
    sunflower           Nature, moderate detail (25fps)
    blue_sky            Sky + tree, simple content (25fps)
    pedestrian_area     Urban pedestrians (25fps)
    station2            Train station (25fps)
    tractor             Slow-moving vehicle (25fps)
    Johnny              Talking head (720p, 60fps)
    KristenAndSara      Two people talking (720p, 60fps)
    FourPeople          Conference room (720p, 60fps)

  4K (Xiph, 2160p):
    crowd_run_4k        Dense crowd (50fps)
    ducks_take_off      Fast takeoff, water spray (50fps)
    old_town_cross      Urban scene (50fps)
    in_to_tree          Camera zoom into tree (50fps)

  4K (UVG, 3840x2160, CC-BY-NC):
    Beauty              Closeup face, hair movement (120fps, ~3.9 GiB)
    Bosphorus           Yacht, bridge, pan right (120fps, ~2.7 GiB)
    Jockey              Horse racing (120fps, ~3.2 GiB)
    ShakeNDry           Dog shaking dry (120fps, ~1.8 GiB)
    HoneyBee            Bee on flowers (120fps, ~3.5 GiB)

  Benchmark Sets (AWCY):
    objective-1-fast    Standard AV1 benchmark (1.9 GB)
    objective-1         Full IETF NETVC set (13 GB)

  Blender Open Movies:
    big_buck_bunny      Animation, 1080p (CC-BY 3.0)
    sintel              Complex animation trailer, 480p (CC-BY 3.0)

  Netflix Open Content (requires AWS CLI, CC-BY 4.0):
    (downloaded via tier medium/large/full)
CLIPS
}

# ── Main ──────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
    case "$1" in
        --tier|-t) TIER="$2"; shift 2 ;;
        --clip|-c) CLIP="$2"; shift 2 ;;
        --list|-l) LIST=true; shift ;;
        --dry-run|-n) DRY_RUN=true; shift ;;
        --help|-h) usage ;;
        *) die "Unknown option: $1" ;;
    esac
done

if $LIST; then
    list_clips
    exit 0
fi

if [[ -z "$TIER" ]] && [[ -z "$CLIP" ]]; then
    echo "No tier or clip specified. Use --help for usage."
    echo ""
    echo "Quick start:"
    echo "  ./scripts/download-assets.sh --tier micro    # ~200 MB, fast"
    echo "  ./scripts/download-assets.sh --tier small    # ~2 GB, standard benchmark"
    echo "  ./scripts/download-assets.sh --list          # see all options"
    exit 1
fi

log "Assets directory: $ASSETS_DIR"
if $USE_S3; then
    log "AWS CLI detected — using S3 for faster downloads"
else
    log "AWS CLI not found — using HTTP (install awscli for faster downloads)"
fi
if $DRY_RUN; then
    log "DRY RUN — no files will be downloaded"
fi
echo ""

if [[ -n "$CLIP" ]]; then
    download_clip "$CLIP"
elif [[ -n "$TIER" ]]; then
    case "$TIER" in
        micro)  tier_micro ;;
        small)  tier_small ;;
        medium) tier_medium ;;
        large)  tier_large ;;
        full)   tier_full ;;
        *) die "Unknown tier: $TIER. Options: micro, small, medium, large, full" ;;
    esac
fi

echo ""
log "Done. Total assets size:"
if [[ -d "$ASSETS_DIR" ]] && ! $DRY_RUN; then
    du -sh "$ASSETS_DIR"
fi
