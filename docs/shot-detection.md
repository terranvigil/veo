# Shot Detection

Shot detection identifies boundaries between shots (continuous sequences from a
single camera setup) in a video. It's the foundation of per-shot encoding  - 
every other step depends on accurate shot boundaries.

## Current Implementation

VEO uses FFmpeg's **scdet** (scene change detection) filter, which performs
bidirectional frame comparison. This is the same class of approach Netflix uses
in their production per-shot encoding pipeline.

```bash
veo per-shot detect -i video.mp4 --threshold 10
```

### How scdet Works

1. For each frame, compute the Mean Absolute Frame Difference (MAFD) against
   both the previous and next frames
2. Normalize the difference to a 0-100 score
3. If the score exceeds the threshold, mark it as a scene change

The bidirectional comparison reduces false positives from flash frames and
camera flashes that would trigger a forward-only detector.

### Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `threshold` | 10 | Scene change sensitivity (0-100). Lower = more shots. |
| `min-duration` | 0.5s | Minimum shot duration. Shorter shots are merged. |

## Alternative Detectors (Not Yet Implemented)

These are documented for future implementation if scdet proves insufficient.

### PySceneDetect AdaptiveDetector

**Accuracy**: Better than FFmpeg for content with gradual lighting changes.
Uses a rolling average of frame differences with adaptive thresholds.

**Integration**: CLI subprocess (`scenedetect` command).

```bash
# Install
pip install scenedetect[opencv]

# Detect shots
scenedetect -i video.mp4 detect-adaptive -t 27 list-scenes
```

**Output format**: CSV with shot start/end timecodes.

**Tradeoffs**:
- (+) Adaptive threshold handles varying content better
- (+) Multiple detector types (Content, Adaptive, Hash, Histogram)
- (-) Python dependency
- (-) ~2x slower than FFmpeg native
- (-) Extra installation step for users

**When to use**: If scdet produces too many false positives on content with
gradual lighting changes or camera movement.

### TransNetV2 (Deep Learning)

**Accuracy**: F1 score 77.9-96.2% depending on dataset. Best at detecting
both hard cuts and gradual transitions (dissolves, fades, wipes).

**Architecture**: 3D separable convolutions analyzing temporal patterns across
frames. 543K parameters - relatively lightweight for a neural network.

**GitHub**: https://github.com/soCzech/TransNetV2

**Integration**: Python + PyTorch subprocess.

```python
from transnetv2 import TransNetV2
model = TransNetV2()
predictions = model.predict_video("video.mp4")
scenes = model.predictions_to_scenes(predictions)
```

**Tradeoffs**:
- (+) Highest accuracy, especially for gradual transitions
- (+) Handles dissolves, fades, wipes that FFmpeg misses
- (-) Requires PyTorch (~2GB dependency)
- (-) ~5-10x slower than FFmpeg
- (-) GPU recommended for reasonable speed on long videos

**When to use**: If your content has many gradual transitions (e.g., film with
dissolves) and you need to detect them for per-shot encoding.

### AutoShot

**Accuracy**: ~4% better than TransNetV2 on standard benchmarks.

**Architecture**: Builds on TransNetV2 with additional temporal modeling.

**Tradeoffs**: Same as TransNetV2 but slightly more accurate and slightly
slower. Not widely adopted yet.

### FFmpeg select Filter (Previous Implementation)

VEO's original detector. Uses forward-only frame comparison.

```bash
ffmpeg -i video.mp4 -vf "select='gt(scene,0.3)',showinfo" -f null -
```

**Tradeoffs**:
- (+) Simplest implementation
- (-) Forward-only comparison (more false positives)
- (-) No confidence scores (binary detect/no-detect)
- (-) Less accurate than scdet for subtle changes

**Not recommended** - scdet is strictly better with no additional cost.

## Comparison Matrix

| Detector | Accuracy | Speed | Dependencies | Gradual Transitions |
|----------|----------|-------|--------------|-------------------|
| **FFmpeg scdet** (current) | Good | Very fast (10-14x realtime) | None (FFmpeg) | Poor |
| PySceneDetect Adaptive | Better | Fast (5-7x realtime) | Python + OpenCV | Fair |
| TransNetV2 | Best | Moderate (1-3x realtime) | Python + PyTorch | Good |
| AutoShot | Best+ | Moderate | Python + PyTorch | Good |
| FFmpeg select (old) | Fair | Very fast | None (FFmpeg) | Poor |

## Do Gradual Transitions Matter?

For per-shot **encoding optimization**, gradual transitions matter less than
you might expect:

- Missing a dissolve just merges two adjacent shots into one longer shot
- The merged shot gets analyzed as a single unit - slightly suboptimal but
  not catastrophic
- **False positives are worse than missed transitions**: a spurious cut creates
  a very short segment with high keyframe overhead
- Netflix uses simple pixel-difference detection (equivalent to scdet) in
  production and achieves 28-37% bitrate savings

**Recommendation**: Start with scdet. Only upgrade to PySceneDetect or
TransNetV2 if you have content with many dissolves AND per-shot analysis of
merged shots is producing measurably worse results.

## Shot Duration Guidelines

| Duration | Behavior |
|----------|----------|
| < 0.5s | Merged with adjacent shot (keyframe overhead dominates) |
| 0.5 - 1s | Minimal benefit from per-shot optimization |
| **1 - 10s** | **Ideal range** - enough content for reliable R-D analysis |
| 10 - 30s | Good, may contain complexity variation within the shot |
| > 30s | Flag as suspicious - may benefit from further splitting |

Netflix's average shot duration is ~4 seconds. Their per-shot system
processes shots individually but collates them into larger chunks (~3 minutes)
for distributed encoding to amortize overhead.

## Future Work

1. **Pluggable detector interface**: Allow switching between scdet,
   PySceneDetect, and TransNetV2 via configuration
2. **Chunked encoding**: Collate adjacent shots into chunks for more efficient
   parallel encoding (Netflix approach)
3. **Confidence-based merging**: Use scdet's confidence scores to decide
   borderline cases (low-confidence boundaries → merge shots)
4. **Two-pass detection**: Fast pass with scdet, then verify low-confidence
   boundaries with a more accurate detector
