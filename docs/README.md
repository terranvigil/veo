# VEO Documentation

## Encoding Optimization

These documents explain the principles, math, and science behind content-aware video encoding optimization - the core of what VEO does.

| Document | Description |
|----------|-------------|
| [Per-Title Encoding](per-title-encoding.md) | Compute an optimal bitrate ladder for each video title using convex hull analysis |
| [Per-Shot Encoding](per-shot-encoding.md) | Extend per-title to per-shot granularity with Trellis bit allocation |
| [Content-Adaptive Encoding (CAE)](content-adaptive-encoding.md) | Broader context-aware approaches: device profiles, network adaptation, ML prediction |
| [Segment-Level Adaptation](segment-level-adaptation.md) | Closed-loop segment-level CRF adaptation for consistent quality |
| [Quality Metrics](quality-metrics.md) | VMAF, PSNR, SSIM, SSIMULACRA2 - how perceptual quality is measured |
| [Rate Control](rate-control.md) | CRF, fixed QP, 2-pass VBR, capped CRF - which mode to use and when |
| [Shot Detection](shot-detection.md) | FFmpeg scdet, PySceneDetect, TransNetV2 - comparison and guidelines |
| [Chunked Encoding](chunked-encoding.md) | Parallel encoding with shot-aware chunking for production workflows |

## Reading Order

If you're new to content-aware encoding, read in this order:

1. **Quality Metrics** - understand how we measure "good" before optimizing for it
2. **Rate Control** - understand the encoding modes that produce the data points
3. **Per-Title Encoding** - the foundational optimization technique
4. **Per-Shot Encoding** - the natural extension to finer granularity
5. **Content-Adaptive Encoding** - the broader picture including ML approaches
6. **Segment-Level Adaptation** - closed-loop CRF tuning per temporal segment
