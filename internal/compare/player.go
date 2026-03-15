package compare

// playerHTML is the self-contained HTML/CSS/JS for the comparison player.
// No external dependencies - everything is inline.
const playerHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>VEO Comparison Player</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: #1a1a2e; color: #e0e0e0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }

.header { padding: 12px 20px; background: #16213e; border-bottom: 1px solid #0f3460; display: flex; justify-content: space-between; align-items: center; }
.header h1 { font-size: 16px; font-weight: 600; }
.stats { font-size: 13px; opacity: 0.8; }
.stats .avg { color: #4ade80; }
.stats .min { color: #f87171; }

.player-container { position: relative; width: 100%; max-width: 1280px; margin: 20px auto; background: #000; aspect-ratio: 16/9; overflow: hidden; cursor: col-resize; }
.player-container video { position: absolute; top: 0; left: 0; width: 100%; height: 100%; object-fit: contain; }
#encoded-video { clip-path: inset(0 0 0 50%); }
.slider-line { position: absolute; top: 0; bottom: 0; width: 2px; background: #fff; left: 50%; z-index: 10; pointer-events: none; }
.slider-handle { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); width: 36px; height: 36px; background: rgba(255,255,255,0.9); border-radius: 50%; z-index: 11; pointer-events: none; display: flex; align-items: center; justify-content: center; font-size: 14px; color: #333; }
.label-ref { position: absolute; top: 8px; left: 8px; background: rgba(0,0,0,0.7); padding: 4px 10px; border-radius: 4px; font-size: 12px; z-index: 5; }
.label-enc { position: absolute; top: 8px; right: 8px; background: rgba(0,0,0,0.7); padding: 4px 10px; border-radius: 4px; font-size: 12px; z-index: 5; }
.vmaf-overlay { position: absolute; bottom: 8px; left: 50%; transform: translateX(-50%); background: rgba(0,0,0,0.8); padding: 6px 14px; border-radius: 4px; font-size: 14px; z-index: 5; font-variant-numeric: tabular-nums; }

.controls { max-width: 1280px; margin: 0 auto; padding: 10px 20px; }
.controls button { background: #0f3460; border: 1px solid #1a1a5e; color: #e0e0e0; padding: 6px 14px; border-radius: 4px; cursor: pointer; margin-right: 6px; font-size: 13px; }
.controls button:hover { background: #1a1a5e; }
.controls button.active { background: #4ade80; color: #000; }
.time-display { font-size: 13px; font-variant-numeric: tabular-nums; float: right; line-height: 32px; }

.timeline { max-width: 1280px; margin: 10px auto; padding: 0 20px; }
.timeline-bar { position: relative; height: 40px; background: #16213e; border-radius: 4px; cursor: pointer; overflow: hidden; }
.timeline-progress { position: absolute; top: 0; left: 0; height: 100%; background: rgba(74,222,128,0.2); }
.vmaf-graph { position: absolute; top: 0; left: 0; width: 100%; height: 100%; }
.vmaf-graph canvas { width: 100%; height: 100%; }
.dip-marker { position: absolute; top: 0; height: 100%; width: 3px; cursor: pointer; z-index: 2; }
.dip-marker.warning { background: rgba(251,191,36,0.7); }
.dip-marker.critical { background: rgba(248,113,113,0.8); }
.dip-marker:hover { width: 5px; }
.dip-tooltip { position: absolute; bottom: 44px; background: rgba(0,0,0,0.9); padding: 4px 8px; border-radius: 4px; font-size: 11px; white-space: nowrap; z-index: 20; pointer-events: none; display: none; }
.dip-marker:hover .dip-tooltip { display: block; }

.dip-list { max-width: 1280px; margin: 10px auto; padding: 0 20px; }
.dip-list h3 { font-size: 14px; margin-bottom: 8px; color: #f87171; }
.dip-list .dips { display: flex; flex-wrap: wrap; gap: 6px; }
.dip-list .dip-btn { background: #16213e; border: 1px solid #333; padding: 4px 10px; border-radius: 4px; cursor: pointer; font-size: 12px; color: #e0e0e0; }
.dip-list .dip-btn.critical { border-color: #f87171; }
.dip-list .dip-btn.warning { border-color: #fbbf24; }
.dip-list .dip-btn:hover { background: #1a1a5e; }
</style>
</head>
<body>

<div class="header">
  <h1>VEO Comparison Player</h1>
  <div class="stats">
    VMAF: avg <span class="avg" id="stat-avg">--</span> |
    min <span class="min" id="stat-min">--</span> |
    max <span id="stat-max">--</span> |
    <span id="stat-dips">0</span> quality dips
  </div>
</div>

<div class="player-container" id="player-container">
  <video id="ref-video" muted playsinline></video>
  <video id="enc-video" muted playsinline></video>
  <div class="slider-line" id="slider-line"></div>
  <div class="slider-handle" id="slider-handle">⟷</div>
  <div class="label-ref">Reference</div>
  <div class="label-enc">Encoded</div>
  <div class="vmaf-overlay" id="vmaf-overlay">VMAF: --</div>
</div>

<div class="controls">
  <button id="btn-play">▶ Play</button>
  <button id="btn-prev">◀ Prev Frame</button>
  <button id="btn-next">Next Frame ▶</button>
  <button id="btn-prev-dip">⚠ Prev Dip</button>
  <button id="btn-next-dip">Next Dip ⚠</button>
  <span class="time-display" id="time-display">0:00 / 0:00</span>
</div>

<div class="timeline">
  <div class="timeline-bar" id="timeline-bar">
    <div class="timeline-progress" id="timeline-progress"></div>
    <canvas id="vmaf-canvas"></canvas>
  </div>
</div>

<div class="dip-list" id="dip-list">
  <h3>Quality Dips (click to seek)</h3>
  <div class="dips" id="dip-buttons"></div>
</div>

<script>
let data = null;
let sliderPos = 0.5;
let isPlaying = false;
let currentDipIdx = -1;

const refVideo = document.getElementById('ref-video');
const encVideo = document.getElementById('enc-video');
const container = document.getElementById('player-container');
const sliderLine = document.getElementById('slider-line');
const sliderHandle = document.getElementById('slider-handle');
const vmafOverlay = document.getElementById('vmaf-overlay');
const timelineBar = document.getElementById('timeline-bar');
const timelineProgress = document.getElementById('timeline-progress');
const vmafCanvas = document.getElementById('vmaf-canvas');

// Load data
fetch('/api/data').then(r => r.json()).then(d => {
  data = d;
  refVideo.src = d.referenceUrl;
  encVideo.src = d.encodedUrl;

  document.getElementById('stat-avg').textContent = d.avgVmaf.toFixed(1);
  document.getElementById('stat-min').textContent = d.minVmaf.toFixed(1);
  document.getElementById('stat-max').textContent = d.maxVmaf.toFixed(1);
  document.getElementById('stat-dips').textContent = d.dips.length;

  if (d.dips.length === 0) {
    document.getElementById('dip-list').style.display = 'none';
  }

  // Draw VMAF timeline graph
  refVideo.addEventListener('loadedmetadata', () => {
    drawVMAFGraph();
    addDipMarkers();
    addDipButtons();
  });
});

// Slider interaction
let isDragging = false;
container.addEventListener('mousedown', (e) => { isDragging = true; updateSlider(e); });
container.addEventListener('mousemove', (e) => { if (isDragging) updateSlider(e); });
document.addEventListener('mouseup', () => { isDragging = false; });

function updateSlider(e) {
  const rect = container.getBoundingClientRect();
  sliderPos = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
  sliderLine.style.left = (sliderPos * 100) + '%';
  sliderHandle.style.left = (sliderPos * 100) + '%';
  encVideo.style.clipPath = 'inset(0 0 0 ' + (sliderPos * 100) + '%)';
}

// Synchronized playback
document.getElementById('btn-play').addEventListener('click', () => {
  if (isPlaying) {
    refVideo.pause(); encVideo.pause();
    document.getElementById('btn-play').textContent = '▶ Play';
  } else {
    refVideo.play(); encVideo.play();
    document.getElementById('btn-play').textContent = '⏸ Pause';
  }
  isPlaying = !isPlaying;
});

// Frame stepping
const fps = 24;
document.getElementById('btn-prev').addEventListener('click', () => {
  seekRelative(-1/fps);
});
document.getElementById('btn-next').addEventListener('click', () => {
  seekRelative(1/fps);
});

function seekRelative(dt) {
  const t = Math.max(0, refVideo.currentTime + dt);
  refVideo.currentTime = t;
  encVideo.currentTime = t;
}

function seekTo(t) {
  refVideo.currentTime = t;
  encVideo.currentTime = t;
  if (isPlaying) {
    refVideo.pause(); encVideo.pause();
    document.getElementById('btn-play').textContent = '▶ Play';
    isPlaying = false;
  }
}

// Dip navigation
document.getElementById('btn-prev-dip').addEventListener('click', () => {
  if (!data || data.dips.length === 0) return;
  currentDipIdx = Math.max(0, currentDipIdx - 1);
  seekTo(data.dips[currentDipIdx].time);
});
document.getElementById('btn-next-dip').addEventListener('click', () => {
  if (!data || data.dips.length === 0) return;
  currentDipIdx = Math.min(data.dips.length - 1, currentDipIdx + 1);
  seekTo(data.dips[currentDipIdx].time);
});

// Timeline click to seek
timelineBar.addEventListener('click', (e) => {
  const rect = timelineBar.getBoundingClientRect();
  const pct = (e.clientX - rect.left) / rect.width;
  seekTo(pct * refVideo.duration);
});

// Update loop
function updateUI() {
  if (!refVideo.duration) { requestAnimationFrame(updateUI); return; }

  const t = refVideo.currentTime;
  const dur = refVideo.duration;

  // Sync encoded video
  if (Math.abs(encVideo.currentTime - t) > 0.1) {
    encVideo.currentTime = t;
  }

  // Timeline progress
  timelineProgress.style.width = ((t / dur) * 100) + '%';

  // Time display
  const fmt = (s) => Math.floor(s/60) + ':' + ('0' + Math.floor(s%60)).slice(-2);
  document.getElementById('time-display').textContent = fmt(t) + ' / ' + fmt(dur);

  // VMAF overlay
  if (data && data.frames.length > 0) {
    const frameIdx = Math.round(t * fps);
    const frame = data.frames[Math.min(frameIdx, data.frames.length - 1)];
    if (frame) {
      const color = frame.vmaf >= 90 ? '#4ade80' : frame.vmaf >= 70 ? '#fbbf24' : '#f87171';
      vmafOverlay.innerHTML = 'VMAF: <span style="color:' + color + '">' + frame.vmaf.toFixed(1) + '</span>';
    }
  }

  requestAnimationFrame(updateUI);
}
requestAnimationFrame(updateUI);

// Draw VMAF graph on timeline canvas
function drawVMAFGraph() {
  if (!data || data.frames.length === 0) return;

  const canvas = vmafCanvas;
  const rect = timelineBar.getBoundingClientRect();
  canvas.width = rect.width * 2;
  canvas.height = rect.height * 2;
  canvas.style.width = rect.width + 'px';
  canvas.style.height = rect.height + 'px';

  const ctx = canvas.getContext('2d');
  ctx.clearRect(0, 0, canvas.width, canvas.height);

  const w = canvas.width;
  const h = canvas.height;
  const frames = data.frames;
  const dur = refVideo.duration || frames[frames.length-1].time;

  // Draw VMAF line
  ctx.beginPath();
  ctx.strokeStyle = 'rgba(74, 222, 128, 0.6)';
  ctx.lineWidth = 2;

  for (let i = 0; i < frames.length; i++) {
    const x = (frames[i].time / dur) * w;
    const y = h - (frames[i].vmaf / 100) * h;
    if (i === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  }
  ctx.stroke();

  // Fill below
  ctx.lineTo(w, h);
  ctx.lineTo(0, h);
  ctx.closePath();
  ctx.fillStyle = 'rgba(74, 222, 128, 0.08)';
  ctx.fill();

  // Draw average line
  ctx.beginPath();
  ctx.strokeStyle = 'rgba(255, 255, 255, 0.3)';
  ctx.lineWidth = 1;
  ctx.setLineDash([4, 4]);
  const avgY = h - (data.avgVmaf / 100) * h;
  ctx.moveTo(0, avgY);
  ctx.lineTo(w, avgY);
  ctx.stroke();
  ctx.setLineDash([]);
}

// Add dip markers to timeline
function addDipMarkers() {
  if (!data || data.dips.length === 0) return;
  const dur = refVideo.duration || 1;

  data.dips.forEach((dip) => {
    const marker = document.createElement('div');
    marker.className = 'dip-marker ' + dip.severity;
    marker.style.left = ((dip.time / dur) * 100) + '%';

    const tooltip = document.createElement('div');
    tooltip.className = 'dip-tooltip';
    tooltip.textContent = 'VMAF ' + dip.vmaf.toFixed(1) + ' @ ' + dip.time.toFixed(1) + 's';
    marker.appendChild(tooltip);

    marker.addEventListener('click', (e) => {
      e.stopPropagation();
      seekTo(dip.time);
    });

    timelineBar.appendChild(marker);
  });
}

// Add dip buttons below timeline
function addDipButtons() {
  if (!data || data.dips.length === 0) return;
  const container = document.getElementById('dip-buttons');

  data.dips.forEach((dip, idx) => {
    const btn = document.createElement('button');
    btn.className = 'dip-btn ' + dip.severity;
    btn.textContent = dip.time.toFixed(1) + 's (VMAF ' + dip.vmaf.toFixed(0) + ')';
    btn.addEventListener('click', () => {
      currentDipIdx = idx;
      seekTo(dip.time);
    });
    container.appendChild(btn);
  });
}

// Keyboard shortcuts
document.addEventListener('keydown', (e) => {
  switch(e.key) {
    case ' ': e.preventDefault(); document.getElementById('btn-play').click(); break;
    case 'ArrowLeft': seekRelative(-1/fps); break;
    case 'ArrowRight': seekRelative(1/fps); break;
    case '[': document.getElementById('btn-prev-dip').click(); break;
    case ']': document.getElementById('btn-next-dip').click(); break;
  }
});
</script>
</body>
</html>`
