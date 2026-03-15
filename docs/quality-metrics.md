# Quality Metrics

Quality metrics are the scoring functions that drive all encoding optimization.
The choice of metric determines what "optimal" means - optimizing for PSNR
produces different results than optimizing for VMAF, because they measure
different things.

## The Fundamental Problem

Video compression is lossy. Every encoded frame differs from the original. The
question is: **how much does it differ, and does a human viewer notice?**

Simple mathematical measures (like pixel-level error) correlate poorly with human
perception. A human might not notice a subtle shift in flat regions but immediately
sees banding in gradients. Perceptual metrics attempt to model the human visual
system to answer the question that actually matters: does this look good?

## PSNR (Peak Signal-to-Noise Ratio)

The simplest and fastest metric. Measures the ratio of the maximum possible signal
power to the noise (error) power.

### Definition

For a single frame with pixel values in range [0, MAX]:

```
MSE = (1/N) * Σ (original[i] - encoded[i])²

PSNR = 10 * log₁₀(MAX² / MSE)  dB
```

Where N is the total number of pixels and MAX is the maximum pixel value (255 for
8-bit, 1023 for 10-bit).

### Properties

- **Range**: typically 30-50 dB for video. Higher is better.
- **Speed**: extremely fast (simple arithmetic over pixels)
- **Perceptual correlation**: poor (0.65 correlation with human MOS scores)
- **Failure modes**: treats all pixels equally; insensitive to structural
  distortion; overly sensitive to mathematically large but perceptually invisible
  changes

### When to Use

PSNR is useful as a fast sanity check or for comparing encodes of the same content
at the same resolution. It should not be used as the primary optimization target.

## SSIM (Structural Similarity Index)

Measures structural similarity between two images by comparing luminance,
contrast, and structure independently.

### Definition

For two image patches x and y:

```
SSIM(x, y) = [l(x,y)]^α · [c(x,y)]^β · [s(x,y)]^γ

Where:
  l(x,y) = (2μₓμᵧ + C₁) / (μₓ² + μᵧ² + C₁)        luminance
  c(x,y) = (2σₓσᵧ + C₂) / (σₓ² + σᵧ² + C₂)        contrast
  s(x,y) = (σₓᵧ + C₃) / (σₓσᵧ + C₃)                structure

  μₓ, μᵧ     = mean pixel intensities
  σₓ, σᵧ     = standard deviations
  σₓᵧ        = cross-covariance
  C₁, C₂, C₃ = small constants for numerical stability
```

The overall SSIM is computed as the mean SSIM across all sliding windows.

### Properties

- **Range**: -1 to 1 (typically 0.85-0.99 for video). Higher is better.
- **Speed**: moderate (sliding window computation)
- **Perceptual correlation**: better than PSNR (0.72 MOS correlation)
- **Improvement**: MS-SSIM (multi-scale) improves accuracy by evaluating at
  multiple resolutions, capturing both fine detail and coarse structure

## VMAF (Video Multi-Method Assessment Fusion)

Developed by Netflix with USC, University of Nantes, and UT Austin. Won a
Technology & Engineering Emmy in 2021. The current industry standard for
streaming quality assessment.

### Architecture

VMAF fuses multiple elementary quality features using a machine learning model
(support vector machine regression) trained on subjective human quality scores:

```
                    ┌──────────┐
  Reference ───────►│   VIF    │───► VIF scores at 4 spatial scales
  Distorted ───────►│          │
                    └──────────┘
                    ┌──────────┐
  Reference ───────►│ DLM/ADM  │───► Detail Loss at multiple scales
  Distorted ───────►│          │
                    └──────────┘
                    ┌──────────┐
  Reference ───────►│  Motion  │───► Temporal activity metric
                    └────┬─────┘
                         │
                         ▼
                    ┌──────────┐
                    │   SVM    │
                    │ Regressor│───► VMAF score (0-100)
                    └──────────┘
```

**VIF (Visual Information Fidelity)**: Measures information fidelity at 4 spatial
scales using natural scene statistics. Based on the premise that the human visual
system has evolved to extract information from natural scenes, so quality can be
measured as information preservation.

**DLM/ADM (Detail Loss Metric / Additive Impairment Metric)**: Separates quality
impairment into detail loss (blurring) and additive impairment (noise, ringing).
Computed at multiple scales.

**Motion**: Temporal difference metric. VMAF weights quality lower for high-motion
frames because viewers are less critical of quality during fast motion (temporal
masking).

### Properties

- **Range**: 0-100. Higher is better. 100 = perceptually indistinguishable from
  the original.
- **Speed**: 6-12x slower than PSNR to compute
- **Perceptual correlation**: highest among standard metrics (0.87+ MOS correlation)
- **Training data**: ~300K subjective quality ratings from Netflix viewers

### VMAF Models

| Model | Use Case |
|-------|----------|
| `vmaf_v0.6.1` | Standard viewing conditions (default) |
| `vmaf_4k_v0.6.1` | 4K TV at 1.5x screen height viewing distance |
| Phone model | Mobile viewing (smaller screen = more forgiving) |
| `vmaf_v0.6.1neg` (NEG) | No Enhancement Gain - penalizes sharpening/contrast boosts that artificially inflate scores |

### Computation in FFmpeg

```bash
ffmpeg -i distorted.mp4 -i reference.mp4 \
  -lavfi '[0:v][1:v]libvmaf=log_fmt=json:log_path=vmaf.json:psnr=1:ssim=1:n_subsample=5' \
  -f null -
```

Key options:
- `n_subsample=5`: measure every 5th frame (5x speedup with minimal accuracy loss)
- `psnr=1:ssim=1`: compute PSNR and SSIM alongside VMAF
- `log_fmt=json`: structured output for programmatic parsing

### Important: Resolution Matching

VMAF requires both inputs to be the **same resolution**. When evaluating a
downscaled encode (e.g., source is 1080p, encode is 720p), you must upscale the
encode back to 1080p before computing VMAF:

```bash
ffmpeg -i encode_720p.mp4 -i reference_1080p.mp4 \
  -lavfi '[0:v]scale=1920:1080:flags=bicubic[d];[d][1:v]libvmaf=...' \
  -f null -
```

## SSIMULACRA2

A newer metric from Jon Sneyers (Cloudinary / JPEG XL project), gaining strong
traction in the open-source encoding community since 2024.

### How It Differs from VMAF

- Not trained on Netflix's viewing data - uses broader psychovisual datasets
  (CID22, TID2013, Kadid10k, KonFiG-IQA)
- Better correlation with subjective scores on modern content types
- No temporal component (measures frame-by-frame only)
- More granular quality scale
- Not biased toward any single service's content distribution

### Properties

- **Range**: roughly -inf to 100. Typical video range: 40-90.
  - 90+: excellent (hard to distinguish from original)
  - 70-90: good (minor artifacts visible on close inspection)
  - 50-70: acceptable (visible artifacts but not distracting)
  - Below 50: poor
- **Speed**: historically slower than VMAF, but GPU implementations (Vship)
  achieve 10-100x speedup
- **Not in FFmpeg**: requires external tool (ssimulacra2_rs)

### Practical Guidance

**Use both VMAF and SSIMULACRA2 when possible.** VMAF is the industry standard
and what published research compares against. SSIMULACRA2 provides a valuable
second opinion, especially for content types underrepresented in Netflix's
training data.

For VEO's per-title analysis, VMAF is the primary optimization target because:
1. Industry-standard comparison baseline
2. Temporal awareness (important for video)
3. Well-understood scoring thresholds (e.g., VMAF 93+ = transparent quality)
4. GPU acceleration available via VMAF-CUDA in FFmpeg

## Metric Comparison

| Metric | MOS Correlation | Speed | Temporal | In FFmpeg |
|--------|----------------|-------|----------|-----------|
| PSNR | 0.65 | Very fast | No | Yes |
| SSIM | 0.72 | Fast | No | Yes |
| MS-SSIM | 0.78 | Fast | No | Yes |
| VMAF | 0.87+ | Moderate | Yes | Yes (libvmaf) |
| SSIMULACRA2 | ~0.90 | Moderate | No | No |

## BD-Rate (Bjontegaard Delta Rate)

BD-Rate is not a quality metric itself but the standard way to compare encoding
efficiency between two configurations. It measures the **average bitrate difference**
between two rate-distortion curves at the same quality level.

```
BD-Rate = -X% means X% bitrate savings at equal quality
BD-Rate = +X% means X% more bitrate needed for equal quality
```

For example, "SVT-AV1 achieves -50% BD-Rate vs x264" means AV1 needs half the
bitrate for the same VMAF score.

BD-Rate is computed by fitting cubic polynomials to each R-D curve, integrating
the area between them, and normalizing. It's the standard metric used in codec
comparison papers and JVET common test conditions.
