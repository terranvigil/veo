# Segment-Level CRF Adaptation

Segment-level CRF adaptation adjusts encoding quality on a **per-segment basis**
(typically 1-2 second segments). Each segment gets its own CRF value based on
content complexity, with closed-loop VMAF verification to maintain consistent
perceptual quality across the video.

This is VEO's third optimization method, building on per-title and per-shot.
It is a practical approximation of true per-frame adaptation (like Beamr CABR)
that works with standard encoders without requiring frame-level re-encoding.

## Concept

Standard encoders already perform some frame-level adaptation internally (AQ,
mbtree, lookahead). Per-frame adaptation goes further by introducing an
**external closed-loop system** that iteratively adjusts encoder parameters based
on quality feedback.

```
┌────────────────────────────────────────────┐
│                Closed Loop                 │
│                                            │
│ Source ──► Encode ──► Measure Quality ──►  │
│             ▲              │               │
│             │              ▼               │
│             └── Adjust Parameters ◄──      │
│                 (if quality > threshold,   │
│                  increase compression)     │
└────────────────────────────────────────────┘
```

The system iteratively re-encodes regions at increasingly aggressive compression
as long as frames remain "perceptually identical" to the original. This squeezes
out every unnecessary bit without crossing the perceptual quality threshold.

## How It Works

### The Iterative Approach (Beamr CABR)

Beamr's Content-Adaptive Bitrate (CABR) is the leading commercial implementation:

```
for each frame (or group of macroblocks):
    1. Encode at the current QP
    2. Measure perceptual quality vs. reference
    3. If quality > threshold (perceptually transparent):
        a. Increase QP (reduce quality/bitrate)
        b. Re-encode
        c. Repeat from step 2
    4. If quality < threshold:
        a. Use the previous (higher quality) encode
        b. Move to next frame
```

This binary-search-like process converges on the **maximum compression that
remains perceptually transparent** for each frame.

### Quality Threshold

The threshold is typically set at the Just Noticeable Difference (JND) - the
smallest quality change a human viewer can perceive. In VMAF terms, this is
approximately:

```
ΔVMAF ≈ 6 points ≈ 1 JND

Rule: if |VMAF(encode) - VMAF(reference)| < JND, the encode is perceptually
transparent.
```

In practice, the threshold is content-dependent. Film grain creates visual
noise that masks compression artifacts, so a higher ΔVMAF may still be
transparent. Clean animation is the opposite - viewers notice artifacts more
easily.

### Macroblock-Level Adaptation

Advanced systems go below the frame level to adapt per-macroblock or per-CTU
(Coding Tree Unit in HEVC/VVC):

```
┌──────┬──────┬──────┬──────┐
│ Sky  │ Sky  │ Sky  │ Sky  │  ← Simple: high QP (fewer bits)
├──────┼──────┼──────┼──────┤
│ Tree │ Face │ Face │ Tree │  ← Important: low QP (more bits)
├──────┼──────┼──────┼──────┤
│Grass │ Body │ Body │Grass │  ← Medium: moderate QP
├──────┼──────┼──────┼──────┤
│Ground│Ground│Ground│Ground│  ← Simple: high QP
└──────┴──────┴──────┴──────┘
```

This is related to but distinct from the encoder's built-in Adaptive Quantization
(AQ). AQ adjusts QP offsets heuristically based on local variance. Per-frame
adaptation uses actual quality measurement in a feedback loop.

## Relationship to Encoder-Internal Features

Modern encoders already have features that adapt to content at the frame level:

| Feature | What It Does | Encoder Support |
|---------|-------------|-----------------|
| **AQ (Adaptive Quantization)** | Adjusts QP per macroblock based on local variance | x264, x265, SVT-AV1 |
| **Mbtree (Macroblock Tree)** | Future reference analysis - allocates more bits to blocks referenced by many future frames | x264 |
| **Lookahead** | Analyzes upcoming frames to make better R-D decisions | All modern encoders |
| **Temporal AQ** | Reduces quality on high-motion frames (temporal masking) | x265 |
| **Film Grain Synthesis** | Strips grain before encoding, re-synthesizes on decode | AV1 (SVT-AV1) |

Per-frame adaptation operates **outside** the encoder, using the encoder as a
black box and adding quality-measurement-driven feedback on top of whatever
internal optimizations the encoder already performs.

## Film Grain Synthesis: A Special Case

AV1's Film Grain Synthesis (FGS) deserves special mention as a per-frame
technique with enormous impact:

```
Traditional encoding:
  Source (with grain) --> Encode grain --> Decode grain
  Problem: grain is extremely expensive (random noise = high entropy)

Film Grain Synthesis:
  Source --> Denoise --> Encode (clean) --> Decode --> Re-add grain
  The grain parameters are transmitted as metadata (~100 bytes/frame)
  The decoder synthesizes matching grain at playback
```

Netflix reports **66% bitrate reduction** on grainy content with AV1 FGS. This
is the single largest per-frame optimization available today, and it's built into
the codec rather than requiring an external system.

## Mathematical Framework

Per-frame adaptation can be formalized as a constrained optimization:

```
For each frame t:
    minimize  R(t)                    (bitrate)
    subject to  D(t) ≤ D_threshold    (distortion below JND)
```

Where D(t) is the perceptual distortion (e.g., 100 - VMAF) and R(t) is the
bitrate of frame t. The constraint ensures quality stays above the perceptual
threshold.

The Lagrangian relaxation:

```
L(t) = R(t) + λ · D(t)
```

The optimal λ represents the "price" of distortion. For per-frame adaptation,
λ is adjusted dynamically based on the quality measurement feedback:

- If quality is well above threshold: increase λ (accept more distortion, save bits)
- If quality is near/below threshold: decrease λ (spend more bits to protect quality)

This is equivalent to adjusting the QP frame-by-frame with quality feedback.

## Practical Considerations

### Compute Cost

Per-frame adaptation is the most expensive approach:

```
Per-title:    N trial encodes × 1 (full video)
Per-shot:     N trial encodes × S shots
Per-frame:    I iterations × F frames (potentially re-encoding each frame multiple times)
```

Beamr addresses this with:
- GPU acceleration (NVIDIA): live 4Kp60 across AVC, HEVC, and AV1
- Convergence typically in 2-4 iterations per frame
- Parallelizable across frames (with constraints from inter-frame dependencies)

### Compatibility

Per-frame adaptation produces standard bitstreams - the decoder doesn't need to
know that parameters were adapted per-frame. This makes it compatible with all
existing players and devices.

### When It's Worth It

Per-frame adaptation provides the most benefit for:
- **Long-form VOD** content with high variability within shots
- **Film grain** heavy content (if FGS is not available)
- **Premium content** where maximum quality-per-bit justifies the compute cost
- **High-view-count content** where CDN savings justify the encoding cost

For content with uniform complexity within shots, per-shot encoding captures
most of the benefit at lower cost.

## Further Reading

- [Beamr CABR Technology](https://beamr.com/cabr)
- [Beamr: Live 4Kp60 Optimized Encoding on NVIDIA](https://blog.beamr.com/2024/09/10/live-4kp60-optimized-encoding-with-beamr-cabr-and-nvidia-holoscan-for-media/)
- Netflix: [AV1 Film Grain Synthesis](https://netflixtechblog.com/av1-scale-film-grain-synthesis-the-awakening-ee09cfdff40b)
