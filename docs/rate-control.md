# Rate Control Modes

Rate control determines how a video encoder allocates bits across frames. The
choice of mode affects both the quality of the output and the predictability of
the bitrate - two properties that are fundamentally in tension.

Understanding rate control is essential for VEO because the per-title pipeline
uses **different modes for different phases**: CRF for analysis, and 2-pass VBR
or capped CRF for delivery.

## The Two-Phase Pattern

Production encoding pipelines (Netflix, YouTube, Mux) follow a consistent pattern:

```
Phase 1: ANALYSIS                  Phase 2: DELIVERY
┌──────────────────────┐          ┌──────────────────────┐
│ Encode at multiple   │          │ Encode at specific   │
│ CRF / QP values      │          │ target bitrates      │
│                      │          │                      │
│ Goal: understand the │ ──────►  │ Goal: hit bitrate    │
│ content's R-D curve  │  ladder  │ targets for ABR      │
│                      │ selection│ streaming            │
│ Mode: CRF or QP      │          │ Mode: 2-pass VBR or  │
│                      │          │ capped CRF           │
└──────────────────────┘          └──────────────────────┘
```

CRF and QP are **analysis tools** - they answer "what bitrate does this content
need for a given quality?" VBR and capped CRF are **delivery tools** - they
produce encodes that meet specific bitrate targets for reliable streaming.

## Modes Explained

### Fixed QP (Constant Quantizer)

Sets a fixed quantization parameter for every macroblock in every frame.

```
Encoder input:  QP = 30
Encoder output: bitrate = f(content_complexity, QP)
```

- The encoder performs **no rate-distortion optimization**. It does not
  redistribute bits between easy and hard frames.
- Bitrate is entirely determined by content complexity at that QP.
- Produces deterministic, reproducible output - same QP always gives same result.
- In x264: `--qp 30` (disables mbtree, AQ, and other R-D features)
- In SVT-AV1: `--rc 0 --enable-adaptive-quantization 0`

**Use case**: Academic research, codec conformance testing. Netflix originally
used fixed QP for convex hull construction because of its determinism.

### CRF (Constant Rate Factor)

Targets constant **perceptual quality** rather than constant quantization.

```
Encoder input:  CRF = 23
Encoder output: bitrate = f(content_complexity, encoder_optimization)
```

- The encoder **does** use R-D optimization: lookahead, adaptive quantization
  (AQ), macroblock tree (mbtree), and other techniques.
- The actual per-frame QP varies significantly. CRF is not QP even though the
  numeric scales overlap.
- Bitrate is variable and unpredictable - a CRF of 23 might produce 2 Mbps for
  a talking head and 15 Mbps for an action scene.
- In x264: `--crf 23` (default)
- In SVT-AV1: `--rc 0 --crf 30` (with adaptive quantization enabled by default)

**Why CRF works well for hull construction**: CRF's R-D optimization makes each
encode more efficient than fixed QP at the same visual quality. The resulting
convex hull is slightly closer to the true Pareto frontier. Netflix confirmed
"results are about the same" as QP - the key insight is that both adequately
sample the R-D space.

### 2-Pass VBR (Variable Bitrate)

Targets a specific **average bitrate** with full knowledge of the content.

```
Pass 1: analyze content complexity → write stats file
Pass 2: encode using stats → hit target average bitrate

Encoder input:  target = 5 Mbps
Encoder output: average bitrate ≈ 5 Mbps (varies per-GOP)
```

- The first pass scans the entire video, measuring complexity per frame/scene.
- The second pass allocates bits based on the complexity map - more bits to hard
  scenes, fewer to easy scenes.
- Produces **predictable average bitrate** with variable instantaneous bitrate.
- In x264: `-b:v 5M -pass 1` then `-b:v 5M -pass 2`
- In SVT-AV1: `--rc 1 --tbr 5000 --pass 1/2`

**Use case**: Production streaming. Once the ladder is selected (with target
bitrates per rung), 2-pass VBR produces the final encodes that hit those targets.

### Capped CRF

CRF encoding with an upper bitrate limit enforced via a decoder buffer model.
The underlying concept is always the same - a leaky bucket that constrains peak
bitrate - but the terminology differs by codec lineage:

- **VBV** (Video Buffering Verifier): the MPEG-2 term
- **HRD** (Hypothetical Reference Decoder): the H.264/H.265 spec term
- **Decoder model**: the AV1 spec term (uses a "smoothing buffer")

In practice, encoder implementations almost universally use "VBV" in their
parameter names regardless of codec (`--vbv-maxrate`, `--vbv-bufsize` in x264
and x265; `--mbr`, `--buf-sz` in SVT-AV1). "HRD" appears as a separate
bitstream-signaling flag (`--nal-hrd` in x264, `--hrd` in x265) that signals
buffer model compliance in the NAL units.

```
Encoder input:  CRF = 23, maxrate = 5 Mbps, bufsize = 10 Mbps
Encoder output: quality ≈ CRF 23 for easy content
                bitrate ≤ 5 Mbps for hard content
```

- Easy content: behaves like pure CRF. May significantly undershoot the cap.
- Hard content: bitrate-limited, quality degrades gracefully.
- In FFmpeg/x264: `-crf 23 -maxrate 5M -bufsize 10M`
- In SVT-AV1: `--crf 30 --mbr 5000`

**Growing in adoption** because it saves bandwidth on easy content (CRF
undershoots) while capping hard content for reliable streaming. Testing shows
capped CRF outperforms VBR by 10-25% in bitrate savings at equivalent quality
for live streaming with SVT-AV1.

### CBR (Constant Bitrate)

Maintains constant instantaneous bitrate.

```
Encoder input:  target = 5 Mbps
Encoder output: bitrate ≈ 5 Mbps at all times
```

- Wastes bits on easy scenes (padding) and compromises quality on hard scenes.
- In FFmpeg/x264: `-b:v 5M -minrate 5M -maxrate 5M -bufsize 10M`

**Use case**: Broadcast television and contribution feeds where bandwidth must be
strictly predictable. Not recommended for VOD. Most live streaming platforms
(including Twitch) actually use VBR or capped CRF rather than true CBR.

### Constrained Quality (VP9/libvpx specific)

A hybrid mode unique to VP9 that sets both a quality floor and a bitrate ceiling.

```
Encoder input:  cq-level = 33, bitrate cap = 2 Mbps
Encoder output: quality ≥ CQ 33 (easy content)
                bitrate ≤ 2 Mbps (hard content)
```

- Conceptually similar to capped CRF but implemented differently in libvpx.
- Google's recommended mode for VP9 VOD. Used by YouTube.
- In FFmpeg: `-b:v 2M -crf 33 -quality good`

## ABR Streaming Constraints

Adaptive bitrate streaming (HLS/DASH) requires that each quality rung have a
**relatively predictable bitrate** because:

1. The player's ABR algorithm selects a rung based on estimated bandwidth.
2. If a rung's bitrate varies wildly, the player may select it but then stall
   on high-bitrate segments.
3. Segment-level bitrate must be bounded for reliable playback.

This is why **pure CRF cannot be used directly for streaming** - bitrate is
unpredictable. The viable modes for ABR delivery are:

| Mode | Suitability | Tradeoff |
|------|------------|----------|
| 2-pass VBR | Excellent | Predictable average bitrate; requires two passes |
| Capped CRF | Good | Quality-focused with cap; may undershoot on easy content |
| CBR | Acceptable | Very predictable but wastes bits; mainly for live |

## What Netflix Uses

Netflix's Dynamic Optimizer follows the two-phase pattern:

1. **Trial encodes**: Fixed QP (migrating to CRF) at multiple resolutions and
   quality levels per shot
2. **Hull construction**: VMAF measurement at each operating point
3. **Trellis optimization**: Select optimal (resolution, QP) per shot per rung
4. **Final encode**: 2-pass VBR targeting the bitrate from the optimization,
   with max buffer ≈ 200% of average target

Netflix's David Ronca (Director of Encoding Technology) confirmed: "We started
with QP and recently migrated to CRF. The results are about the same."

## VEO's Approach

VEO uses **CRF for the analysis phase** (trial encodes to build the convex hull)
and supports both **2-pass VBR and capped CRF for the delivery phase** (final
encodes at selected operating points).

CRF is preferred over fixed QP for hull construction because:
- CRF's R-D optimization produces more efficient encodes
- The hull from CRF encodes is slightly closer to the true Pareto frontier
- Practical difference from QP is small (as Netflix confirmed)
- CRF is the standard mode most users are familiar with

## Further Reading

- Netflix: [Dynamic Optimizer Framework](https://netflixtechblog.com/dynamic-optimizer-a-perceptual-video-encoding-optimization-framework-e19f1e3a277f)
- slhck: [Understanding Rate Control Modes](https://slhck.info/video/2017/03/01/rate-control.html)
- slhck: [CRF Guide](https://slhck.info/video/2017/02/24/crf-guide.html)
- Streaming Learning Center: [Best SVT-AV1 Bitrate Control for Live Streaming](https://streaminglearningcenter.com/articles/best-svt-av1-bitrate-control-technique-for-live-streaming.html)
