// veo compare: two encodes of one source, locked to the same frame.
// Reads manifest.json from beside the page; nothing else is fetched.
(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
  const SVG = 'http://www.w3.org/2000/svg';

  // labels come from the manifest, so every node is built with textContent
  function h(tag, attrs, ...kids) {
    const svg = ['svg', 'path', 'line', 'rect', 'text', 'g', 'circle', 'use'].includes(tag);
    const node = svg ? document.createElementNS(SVG, tag) : document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v == null) continue;
      if (k === 'class' && !svg) node.className = v;
      else if (k === 'text') node.textContent = v;
      else node.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids.flat()) {
      if (kid == null || kid === false) continue;
      node.append(kid.nodeType ? kid : document.createTextNode(kid));
    }
    return node;
  }
  const icon = (id) => h('svg', { 'aria-hidden': 'true' }, h('use', { href: '#' + id }));
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

  const ZOOMS = [1, 2, 4, 8];
  const RATES = [0.25, 0.5, 1];
  const KINDS = {
    'dip-a': { stream: 'a', shape: 'dip', verb: 'dip' },
    'dip-b': { stream: 'b', shape: 'dip', verb: 'dip' },
    'lead-a': { stream: 'a', shape: 'lead', verb: 'ahead' },
    'lead-b': { stream: 'b', shape: 'lead', verb: 'ahead' },
  };

  const S = {
    m: null, fps: 24, frames: 1,
    frame: 0, playing: false, rate: 1, loop: null,
    view: 'split', pair: ['a', 'b'], solo: 'a', peek: false,
    zoom: 1, cx: 0.5, cy: 0.5, split: 0.5,
    filter: 'all', hover: null, sel: { a: null, b: null },
  };
  // every encode in the manifest by its id, and the reference
  const encodes = {};
  let reference = null;
  // what is on screen: streams.a and streams.b are the two chosen encodes, streams.ref the reference.
  // each is { id: 'a' | 'b' | 'ref', eid, label, ..., video, layer, state: 'ok' | 'proxy' | 'dead' }
  const streams = {};
  const everything = () => [...Object.values(encodes), reference].filter(Boolean);
  let momentCache = null;
  let pendingSeek = false;
  let chart = null;   // geometry and nodes of the timeline, rebuilt on resize
  let painted = -1;

  // ---------- formatting ----------

  const pad = (n, w = 2) => String(n).padStart(w, '0');
  function timecode(frame) {
    const t = frame / S.fps;
    const min = Math.floor(t / 60);
    return `${pad(min)}:${pad(Math.floor(t % 60))}.${pad(Math.floor((t % 1) * 1000 + 1e-6), 3)}`;
  }
  function fmtBytes(n) {
    if (n >= 1e9) return (n / 1e9).toFixed(2) + ' GB';
    if (n >= 1e6) return (n / 1e6).toFixed(2) + ' MB';
    return (n / 1e3).toFixed(1) + ' kB';
  }
  const fmtKbps = (k) => (k >= 10000 ? [(k / 1000).toFixed(1), 'Mb/s'] : [Math.round(k).toLocaleString('en-US'), 'kb/s']);
  const fmtScore = (v) => (v == null ? '–' : v.toFixed(2));
  const signed = (v, places = 2) => (v > 0 ? '+' : v < 0 ? '−' : '±') + Math.abs(v).toFixed(places);
  const scoreAt = (id, frame) => {
    const s = streams[id] && streams[id].scores;
    return s && frame < s.length ? s[frame] : null;
  };
  const key = (id, small) => h('span', { class: `key ${id}${small ? ' sm' : ''}`, 'aria-hidden': 'true', text: id === 'ref' ? 'S' : id.toUpperCase() });

  // ---------- load ----------

  async function boot() {
    let m;
    try {
      const res = await fetch('manifest.json', { cache: 'no-cache' });
      if (!res.ok) throw new Error(`manifest.json: HTTP ${res.status}`);
      m = await res.json();
    } catch (err) {
      fatal('Couldn’t load manifest.json',
        location.protocol === 'file:'
          ? 'Browsers block this page from reading files when it is opened from disk. Serve the folder instead, for example with python3 -m http.server, and open the address it prints.'
          : String(err.message || err));
      return;
    }
    if (!Array.isArray(m.encodes) || !m.encodes.length || !m.encodes[0].src) {
      fatal('This manifest has no encodes', 'manifest.json needs an encodes list, each entry with an id and a src.');
      return;
    }
    S.m = m;
    S.fps = m.fps > 0 ? m.fps : 24;
    mediaBase = new URL(m.mediaBase || 'media/', location.href).href;
    for (const e of m.encodes) encodes[e.id] = { scores: null, ...e, eid: e.id, id: null };
    if (m.reference) reference = { scores: null, ...m.reference, eid: 'reference', id: 'ref' };
    S.frames = Math.max(1, m.frames || 0, ...everything().map((s) => (s.scores ? s.scores.length : 0)));
    const ids = Object.keys(encodes);
    const want = m.compare || {};
    S.sel.a = encodes[want.a] ? want.a : ids[0];
    S.sel.b = encodes[want.b] && want.b !== S.sel.a ? want.b : ids.find((id) => id !== S.sel.a) || null;
    S.pair = S.sel.b ? ['a', 'b'] : reference ? ['ref', 'a'] : ['a'];
    if (S.pair.length < 2) S.view = 'solo';

    readHash();
    assign();
    document.title = `${m.title} · veo compare`;
    $('#title').textContent = m.title;
    const dur = timecode(S.frames).slice(0, 5);
    $('#meta').textContent = [
      m.subtitle,
      m.width && `${m.width}×${m.height}`,
      `${+S.fps.toFixed(3)} fps`,
      `${S.frames.toLocaleString('en-US')} frames`,
      dur,
    ].filter(Boolean).join('  ·  ');
    $('#ftotal').textContent = ' / ' + (S.frames - 1);

    buildStage();
    buildControls();
    buildVariants();
    refresh();
    new ResizeObserver(() => { layout(); drawChart(); }).observe($('#stage'));
    new ResizeObserver(drawChart).observe($('#chart'));
    drawChart();
    seek(S.frame);
    requestAnimationFrame(tick);
  }

  function fatal(title, text) {
    const box = $('#fatal');
    box.replaceChildren(h('strong', { text: title }), h('p', { text }));
    box.hidden = false;
  }

  // ---------- video ----------

  function canPlay(mime) {
    return !mime || document.createElement('video').canPlayType(mime) !== '';
  }

  function buildStage() {
    const stage = $('#stage');
    for (const s of everything()) {
      const video = h('video', { muted: true, playsinline: true, preload: 'auto', disablepictureinpicture: true, 'aria-label': s.label });
      video.muted = true;
      s.video = video;
      s.msg = h('div', { class: 'msg', hidden: true });
      s.layer = h('div', { class: 'layer' }, video, s.msg);
      stage.prepend(s.layer);

      video.addEventListener('error', () => { if (s.state) degrade(s); });
      video.addEventListener('loadedmetadata', () => {
        // the picture's own shape, which also covers non-square pixels
        if (video.videoWidth) stage.style.setProperty('--ar', `${video.videoWidth} / ${video.videoHeight}`);
        seek(S.frame, { quiet: true });
      });
      video.addEventListener('loadeddata', () => {
        $('#loading').hidden = true;
        if (s.t0 == null) {
          // native HLS keeps the segments' own timeline in some versions and starts it at zero in others
          const start = video.buffered.length ? video.buffered.start(0) : 0;
          const shift = (s.segments && s.segments.start) || 0;
          s.t0 = Math.abs(start - shift) < 0.03 ? shift : start < 0.03 ? 0 : start;
          seek(S.frame, { quiet: true });
        }
      });
      video.addEventListener('seeked', () => {
        if (pendingSeek && !S.playing && !live().some((x) => x.video.seeking)) { pendingSeek = false; seek(S.frame, { quiet: true }); }
      });
    }
    for (const s of Object.values(streams)) load(s);
  }


  // ---------- delivery ----------
  // A stream reaches the <video> one of three ways, tried in this order:
  //   Safari and iOS play the HLS playlist themselves;
  //   other browsers get the same segments through Media Source Extensions;
  //   anything else plays the whole progressive file.
  // Every path has to agree on which picture is frame N, so each stream keeps
  // t0: where frame 0 sits on that <video>'s own clock.

  let mediaBase = location.href;
  const url = (path) => new URL(path, mediaBase).href;
  const MS = window.ManagedMediaSource || window.MediaSource;
  const KEEP_BEHIND = 8;     // seconds of picture kept behind the playhead
  const KEEP_AHEAD = 24;     // and fetched ahead of it
  const BUFFER_BYTES = 96e6; // per stream, under what browsers allow a source buffer

  function load(s) {
    if (s.state || !s.video) return;
    const v = s.video;
    const seg = s.segments;
    const apple = navigator.vendor === 'Apple Computer, Inc.';
    const hls = seg && seg.playlist && v.canPlayType('application/vnd.apple.mpegurl') !== '' && canPlay(s.mime);
    const mse = seg && seg.init && MS && s.mime && MS.isTypeSupported(s.mime);
    s.t0 = 0;
    if (hls && (apple || !mse)) {
      s.state = 'ok';
      s.via = 'hls';
      s.t0 = null; // measured once the first segment is in
      v.src = url(seg.playlist);
    } else if (mse) {
      s.state = 'ok';
      s.via = 'mse';
      startMse(s);
    } else if (s.src && canPlay(s.mime)) {
      s.state = 'ok';
      s.via = 'file';
      v.src = url(s.src);
    } else {
      degrade(s);
    }
  }

  function startMse(s) {
    const v = s.video;
    const seg = s.segments;
    const ms = new MS();
    const st = { ms, sb: null, have: new Map(), busy: false, ctrl: null, fetching: -1, closed: false };
    s.mse = st;
    if (window.ManagedMediaSource) v.disableRemotePlayback = true;
    v.src = URL.createObjectURL(ms);
    ms.addEventListener('sourceopen', async () => {
      try {
        const last = seg.list[seg.list.length - 1];
        ms.duration = last.start + last.duration;
        st.sb = ms.addSourceBuffer(s.mime);
        // moves the stream so its first frame is at zero, whatever shift the muxer gave this encode
        st.sb.timestampOffset = -(seg.start || 0);
        await append(st, await grab(st, seg.init));
        st.ready = true;
        pump(s);
      } catch (err) {
        if (!st.closed) degrade(s);
      }
    }, { once: true });
  }

  function stopMse(s) {
    const st = s.mse;
    if (!st) return;
    st.closed = true;
    if (st.ctrl) st.ctrl.abort();
    s.mse = null;
  }

  async function grab(st, path) {
    st.ctrl = new AbortController();
    const res = await fetch(url(path), { signal: st.ctrl.signal });
    if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
    return res.arrayBuffer();
  }
  function settle(sb, act) {
    return new Promise((resolve, reject) => {
      const done = () => { sb.removeEventListener('error', fail); resolve(); };
      const fail = () => { sb.removeEventListener('updateend', done); reject(new Error('source buffer error')); };
      sb.addEventListener('updateend', done, { once: true });
      sb.addEventListener('error', fail, { once: true });
      act();
    });
  }
  const append = (st, data) => settle(st.sb, () => st.sb.appendBuffer(data));

  const segAt = (seg, t) => {
    const list = seg.list;
    for (let i = list.length - 1; i >= 0; i--) if (t >= list[i].start - 1e-3) return i;
    return 0;
  };

  // the next segment this stream is missing: the one under the playhead first, then forward
  function wanted(s, t) {
    const st = s.mse;
    const list = s.segments.list;
    const at = segAt(s.segments, t);
    const b = s.video.buffered;
    let inBuffer = false;
    for (let i = 0; i < b.length; i++) if (t >= b.start(i) - 0.05 && t < b.end(i) + 0.05) inBuffer = true;
    // the browser may have dropped what we appended
    if (st.have.has(at) && !inBuffer && !s.video.seeking) st.have.delete(at);
    if (!st.have.has(at)) return at;
    let held = 0;
    for (const bytes of st.have.values()) held += bytes;
    for (let i = at + 1; i < list.length && list[i].start < t + KEEP_AHEAD; i++) {
      if (st.have.has(i)) continue;
      return held + list[i].bytes > BUFFER_BYTES ? -1 : i;
    }
    return -1;
  }

  async function evict(s, t) {
    const st = s.mse;
    const list = s.segments.list;
    for (const i of [...st.have.keys()]) {
      const seg = list[i];
      if (seg.start + seg.duration < t - KEEP_BEHIND || seg.start > t + KEEP_AHEAD + 10) {
        st.have.delete(i);
        await settle(st.sb, () => st.sb.remove(seg.start, seg.start + seg.duration));
      }
    }
  }

  async function pump(s) {
    const st = s.mse;
    if (!st || !st.ready || st.busy || st.closed) return;
    st.busy = true;
    try {
      for (;;) {
        const t = (S.frame + 0.5) / S.fps;
        const i = wanted(s, t);
        if (i < 0 || st.closed) break;
        st.fetching = i;
        let data;
        try {
          data = await grab(st, s.segments.list[i].src);
        } catch (err) {
          if (err.name === 'AbortError' && !st.closed) continue; // the playhead moved; ask again
          throw err;
        }
        st.fetching = -1;
        await evict(s, t);
        await append(st, data);
        st.have.set(i, s.segments.list[i].bytes);
        // without this the last frames wait for data that will never come
        if (i === s.segments.list.length - 1 && st.ms.readyState === 'open' && !st.sb.updating) {
          try { st.ms.endOfStream(); } catch (err) { /* reopened by a later append */ }
        }
      }
    } catch (err) {
      if (!st.closed) degrade(s);
    } finally {
      st.busy = false;
      st.fetching = -1;
    }
  }

  // drops a fetch the playhead has left behind, then fetches for where it is now
  function chase(s, t) {
    const st = s.mse;
    if (!st) return;
    if (st.ctrl && st.fetching >= 0 && st.fetching !== segAt(s.segments, t)) st.ctrl.abort();
    pump(s);
  }

  // puts the chosen encodes on the A and B sides
  function assign() {
    for (const e of Object.values(encodes)) e.id = null;
    for (const k of Object.keys(streams)) delete streams[k];
    for (const side of ['a', 'b']) {
      const e = encodes[S.sel[side]];
      if (e) { e.id = side; streams[side] = e; }
    }
    if (reference) streams.ref = reference;
    for (const e of Object.values(encodes)) if (!e.id && e.video) e.video.pause();
    for (const s of Object.values(streams)) load(s);
    momentCache = null;
    S.filter = 'all';
  }

  function pick(side, eid) {
    if (!encodes[eid] || S.sel[side] === eid) return;
    const other = side === 'a' ? 'b' : 'a';
    if (S.sel[other] === eid) S.sel[other] = S.sel[side];
    S.sel[side] = eid;
    if (S.playing) pause();
    assign();
    refresh();
    say(`${streams[side].label} on side ${side.toUpperCase()}`);
    saveHash();
  }

  // redraws everything that depends on which encodes are chosen
  function refresh() {
    renderScore();
    renderJump();
    buildNotes();
    layout();
    drawChart();
    const none = moments().length === 0;
    $('#btn-jprev').disabled = none;
    $('#btn-jnext').disabled = none;
    seek(S.frame, { quiet: true });
  }

  // steps a stream down: the encode itself, then its proxy, then a message
  function degrade(s) {
    const fb = s.fallback;
    const streamed = s.via === 'mse' || s.via === 'hls';
    stopMse(s);
    s.t0 = 0;
    if (streamed && s.state === 'ok' && s.src && canPlay(s.mime)) {
      s.via = 'file';
      s.video.src = url(s.src);
    } else if (s.state !== 'proxy' && s.state !== 'dead' && fb && canPlay(fb.mime)) {
      s.state = 'proxy';
      s.via = 'file';
      s.video.src = url(fb.src);
    } else {
      s.state = 'dead';
      s.video.removeAttribute('src');
      s.video.hidden = true;
      const hevc = /hvc1|hev1/.test(s.mime || '');
      s.msg.replaceChildren(
        h('strong', { text: `This browser can’t play ${s.label}` }),
        h('p', {
          text: hevc
            ? 'It is HEVC, and this browser has no HEVC decoder. Safari plays it, and so do Chrome and Edge on hardware that decodes HEVC. The quality timeline below works either way.'
            : `It can’t decode ${s.codec || 'this format'}. The quality timeline below works either way.`,
        }));
      s.msg.hidden = false;
      $('#loading').hidden = true;
    }
    renderTags();
  }

  const live = () => Object.values(streams).filter((s) => s.state !== 'dead');
  function master() {
    const order = [...S.pair, 'a', 'b', 'ref'];
    for (const id of order) {
      const s = streams[id];
      if (s && s.state !== 'dead' && s.t0 != null && s.video.readyState >= 1) return s;
    }
    return null;
  }

  function seek(frame, opts = {}) {
    frame = clamp(Math.round(frame), 0, S.frames - 1);
    S.frame = frame;
    // the middle of the frame, so rounding in the browser can't land on a neighbor
    const t = (frame + 0.5) / S.fps;
    for (const s of live()) {
      const v = s.video;
      chase(s, t);
      if (v.readyState < 1 || s.t0 == null) continue;
      // while scrubbing, wait for the last seek to land before asking for another
      if (opts.soft && v.seeking) { pendingSeek = true; continue; }
      const end = Number.isFinite(v.duration) ? v.duration - 0.001 : Infinity;
      const to = Math.min(t + s.t0, end);
      if (Math.abs(v.currentTime - to) > 1e-4) v.currentTime = to;
    }
    vclock.at = performance.now();
    vclock.frame = frame;
    if (S.loop && !opts.keepLoop && (frame < S.loop[0] || frame > S.loop[1])) setLoop(true);
    paint();
    if (!opts.quiet) saveHash();
  }

  // stands in for a video clock when no stream can be decoded here
  const vclock = { at: 0, frame: 0 };

  function play() {
    if (S.playing) return;
    if (S.frame >= S.frames - 1) seek(S.loop ? S.loop[0] : 0, { keepLoop: true });
    S.playing = true;
    for (const s of live()) {
      s.video.playbackRate = S.rate;
      const p = s.video.play();
      if (p) p.catch(() => {});
    }
    vclock.at = performance.now();
    vclock.frame = S.frame;
    renderPlay();
  }

  function pause() {
    if (!S.playing) return;
    S.playing = false;
    for (const s of live()) s.video.pause();
    seek(S.frame);
    renderPlay();
  }
  const toggle = () => (S.playing ? pause() : play());

  function renderPlay() {
    $('#play-icon').setAttribute('href', S.playing ? '#i-pause' : '#i-play');
    $('#btn-play').setAttribute('aria-label', S.playing ? 'Pause' : 'Play');
  }

  function tick() {
    if (S.playing) {
      const lead = master();
      const mv = lead && lead.video;
      let frame;
      if (mv) {
        const now = mv.currentTime - lead.t0;
        frame = Math.floor(now * S.fps + 0.01);
        if (frame % 12 === 0) for (const s of live()) pump(s);
        for (const s of live()) {
          const v = s.video;
          if (v === mv || v.readyState < 2 || s.t0 == null) continue;
          const drift = v.currentTime - s.t0 - now;
          if (Math.abs(drift) > 0.2) v.currentTime = now + s.t0;
          else if (Math.abs(drift) > 0.4 / S.fps) v.playbackRate = S.rate * (drift > 0 ? 0.9 : 1.1);
          else v.playbackRate = S.rate;
          if (v.paused && !v.ended) { const p = v.play(); if (p) p.catch(() => {}); }
        }
      } else {
        frame = vclock.frame + Math.floor(((performance.now() - vclock.at) / 1000) * S.fps * S.rate);
      }
      const end = S.loop ? S.loop[1] : S.frames - 1;
      if (frame >= end || (mv && mv.ended)) {
        if (S.loop) {
          seek(S.loop[0], { keepLoop: true, quiet: true });
        } else {
          S.frame = S.frames - 1;
          pause();
        }
      } else {
        S.frame = clamp(frame, 0, S.frames - 1);
      }
    }
    if (S.frame !== painted) paint();
    requestAnimationFrame(tick);
  }

  function setRate(rate) {
    S.rate = rate;
    for (const s of live()) s.video.playbackRate = rate;
    vclock.at = performance.now();
    vclock.frame = S.frame;
    for (const b of $$('#seg-rate button')) b.setAttribute('aria-pressed', String(+b.dataset.rate === rate));
  }

  function setLoop(on) {
    const span = Math.round(S.fps);
    S.loop = on ? [Math.max(0, S.frame - span), Math.min(S.frames - 1, S.frame + span)] : null;
    $('#btn-loop').setAttribute('aria-pressed', String(!!S.loop));
    drawLoop();
  }

  // ---------- stage layout ----------

  function shown() {
    if (S.peek && streams.ref) return ['ref'];
    if (S.view === 'solo' || S.pair.length < 2) return [streams[S.solo] ? S.solo : S.pair[0]];
    return S.pair;
  }

  function layout() {
    const stage = $('#stage');
    const ids = shown();
    const view = ids.length === 1 ? 'solo' : S.view;
    stage.dataset.view = view;
    stage.classList.toggle('zoomed', S.zoom > 1);

    const half = 0.5 / S.zoom;
    S.cx = clamp(S.cx, half, 1 - half);
    S.cy = clamp(S.cy, half, 1 - half);

    for (const s of everything()) {
      const i = s.id ? ids.indexOf(s.id) : -1;
      const st = s.layer.style;
      st.visibility = i < 0 ? 'hidden' : 'visible';
      st.zIndex = i < 0 ? 0 : i + 1;
      st.clipPath = '';
      st.left = '0';
      st.width = '100%';
      if (i < 0) continue;
      if (view === 'split' && i === 1) st.clipPath = `inset(0 0 0 ${(S.split * 100).toFixed(3)}%)`;
      if (view === 'side') {
        st.width = 'calc(50% - 1px)';
        st.left = i === 0 ? '0' : 'calc(50% + 1px)';
      }
      const w = s.layer.clientWidth;
      const hh = s.layer.clientHeight;
      const tx = w / 2 - S.cx * w * S.zoom;
      const ty = hh / 2 - S.cy * hh * S.zoom;
      s.video.style.transform = S.zoom === 1 ? '' : `translate(${tx.toFixed(2)}px, ${ty.toFixed(2)}px) scale(${S.zoom})`;
    }

    const div = $('#divider');
    div.hidden = view !== 'split';
    div.style.left = (S.split * 100).toFixed(3) + '%';
    div.setAttribute('aria-valuenow', Math.round(S.split * 100));

    const chip = $('#zoomchip');
    chip.hidden = S.zoom === 1;
    chip.textContent = S.zoom + '×';

    for (const b of $$('#seg-view button')) b.setAttribute('aria-pressed', String(b.dataset.view === S.view));
    for (const b of $$('#seg-zoom button')) b.setAttribute('aria-pressed', String(+b.dataset.zoom === S.zoom));
    for (const b of $$('#seg-solo button')) b.setAttribute('aria-pressed', String(b.dataset.solo === S.solo));
    $('#seg-solo').hidden = S.view !== 'solo' || Object.keys(streams).length < 2;
    for (const b of $$('#seg-solo button')) b.title = streams[b.dataset.solo] ? streams[b.dataset.solo].label : '';
    $('#btn-flip').hidden = S.pair.length < 2;
    $('#btn-peek').hidden = !streams.ref || S.pair.includes('ref');
    $('#btn-peek').setAttribute('aria-pressed', String(S.peek));
    renderTags();
  }

  function renderTags() {
    const ids = shown();
    const fill = (node, id) => {
      const s = streams[id];
      node.hidden = !s;
      if (!s) return;
      node.replaceChildren(key(id, true), h('span', { class: 'name', text: s.label }));
      if (s.state === 'proxy') {
        const note = h('span', { class: 'proxy' }, icon('i-warn'), 'proxy');
        node.append(note);
        node.title = `This browser can’t decode ${s.codec || 'this stream'}, so it is playing a stand-in file. Don’t judge fine detail from it.`;
      }
    };
    fill($('#tag-l'), ids[0]);
    fill($('#tag-r'), ids[1]);
  }

  function setView(view) {
    if (view !== 'solo' && S.pair.length < 2) return;
    S.view = view;
    layout();
    saveHash();
  }
  function setSolo(id) {
    if (!streams[id]) return;
    S.solo = id;
    S.view = 'solo';
    layout();
    saveHash();
  }
  function flip() {
    if (S.pair.length < 2) return;
    if (S.view === 'solo') {
      S.solo = S.solo === S.pair[0] ? S.pair[1] : S.pair[0];
    } else {
      S.pair.reverse();
      renderScore();
    }
    layout();
    say(S.view === 'solo' ? `Showing ${streams[S.solo].label}` : `${streams[S.pair[0]].label} on the left`);
    saveHash();
  }
  function setPeek(on) {
    if (!streams.ref || S.peek === on) return;
    S.peek = on;
    layout();
  }
  function setZoom(zoom, px, py) {
    zoom = clamp(zoom, 1, 8);
    if (px != null && zoom !== S.zoom) {
      // keep the point under the pointer where it is
      const ux = S.cx + (px - 0.5) / S.zoom;
      const uy = S.cy + (py - 0.5) / S.zoom;
      S.cx = ux - (px - 0.5) / zoom;
      S.cy = uy - (py - 0.5) / zoom;
    }
    S.zoom = zoom;
    if (zoom === 1) { S.cx = 0.5; S.cy = 0.5; }
    layout();
    saveHash();
  }
  const zoomStep = (dir, px, py) => {
    const i = clamp(ZOOMS.indexOf(S.zoom) + dir, 0, ZOOMS.length - 1);
    setZoom(ZOOMS[i], px, py);
  };

  // pointer position as a fraction of the pane it is over
  function paneAt(e) {
    const ids = shown();
    for (const id of ids.slice().reverse()) {
      const r = streams[id].layer.getBoundingClientRect();
      if (e.clientX >= r.left && e.clientX <= r.right) {
        return { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height, w: r.width, h: r.height };
      }
    }
    const r = $('#stage').getBoundingClientRect();
    return { x: 0.5, y: 0.5, w: r.width, h: r.height };
  }

  function bindStage() {
    const stage = $('#stage');
    const divider = $('#divider');
    let drag = null;

    const splitTo = (e) => {
      const r = stage.getBoundingClientRect();
      S.split = clamp((e.clientX - r.left) / r.width, 0, 1);
      layout();
    };

    stage.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      const onDivider = e.target.closest('#divider');
      if (onDivider || (S.zoom === 1 && stage.dataset.view === 'split')) {
        drag = { kind: 'split' };
        splitTo(e);
      } else if (S.zoom > 1) {
        const p = paneAt(e);
        drag = { kind: 'pan', x: e.clientX, y: e.clientY, cx: S.cx, cy: S.cy, w: p.w, h: p.h };
        stage.classList.add('panning');
      } else {
        return;
      }
      stage.setPointerCapture(e.pointerId);
      e.preventDefault();
    });
    stage.addEventListener('pointermove', (e) => {
      if (!drag) return;
      if (drag.kind === 'split') {
        splitTo(e);
      } else {
        S.cx = drag.cx - (e.clientX - drag.x) / (drag.w * S.zoom);
        S.cy = drag.cy - (e.clientY - drag.y) / (drag.h * S.zoom);
        layout();
      }
    });
    const drop = () => {
      if (!drag) return;
      drag = null;
      stage.classList.remove('panning');
      saveHash();
    };
    stage.addEventListener('pointerup', drop);
    stage.addEventListener('pointercancel', drop);

    let wheel = 0;
    stage.addEventListener('wheel', (e) => {
      e.preventDefault();
      wheel += e.deltaY;
      if (Math.abs(wheel) < 60) return;
      const p = paneAt(e);
      zoomStep(wheel < 0 ? 1 : -1, p.x, p.y);
      wheel = 0;
    }, { passive: false });
    stage.addEventListener('dblclick', (e) => {
      if (e.target.closest('#divider')) return;
      const p = paneAt(e);
      setZoom(S.zoom === 1 ? 2 : 1, p.x, p.y);
    });

    divider.addEventListener('keydown', (e) => {
      const step = e.key === 'ArrowLeft' ? -0.02 : e.key === 'ArrowRight' ? 0.02 : 0;
      if (!step) return;
      e.preventDefault();
      e.stopPropagation();
      S.split = clamp(S.split + step, 0, 1);
      layout();
    });
  }

  // ---------- the two encodes ----------

  function renderScore() {
    const box = $('#score');
    const label = S.m.metric.label;
    const side = (id, cls) => {
      const s = streams[id];
      const many = id !== 'ref' && Object.keys(encodes).length > 2;
      let name = h('strong', { text: s.label });
      if (many) {
        name = h('select', { id: 'pick-' + id, 'aria-label': `Encode on side ${id.toUpperCase()}`, title: 'Choose the encode on this side' },
          Object.values(encodes).map((e) => h('option', { value: e.eid, selected: e === s, text: e.label })));
        name.value = s.eid;
        name.addEventListener('change', () => pick(id, name.value));
      }
      const num = (value, caption, opt) => h('div', { class: 'num' + (opt === true ? ' opt' : opt ? ' ' + opt : '') }, h('b', { text: value }), h('span', { text: caption }));
      return h('div', { class: 'side ' + cls },
        key(id),
        h('div', { class: 'who' },
          name,
          h('span', { text: [s.codec, s.detail].filter(Boolean).join(' · ') || ' ' })),
        h('div', { class: 'nums' },
          s.bytes ? num(fmtBytes(s.bytes), 'file size') : null,
          s.bitrateKbps ? num(...fmtKbps(s.bitrateKbps), true) : null,
          s.score ? num(s.score.mean.toFixed(2), `mean ${label}`) : null,
          s.score ? num(s.score.p5.toFixed(2), 'lowest 5%', 'wide') : null));
    };

    const [l, r] = S.pair;
    const kids = [side(l, 'l')];
    const a = streams.a;
    const b = streams.b;
    if (b && S.pair.includes('a') && S.pair.includes('b')) {
      const fair = h('div', { class: 'fair', role: 'note', 'aria-label': 'How the two compare' });
      if (a.bytes && b.bytes) {
        const diff = b.bytes / a.bytes - 1;
        const matched = Math.abs(diff) <= 0.02;
        fair.append(h('div', { class: matched ? 'ok' : 'off' },
          icon(matched ? 'i-check' : 'i-warn'),
          matched ? 'Same file size' : 'File sizes differ',
          h('b', { text: matched ? `within ${(Math.abs(diff) * 100).toFixed(1)}%` : `B ${signed(diff * 100, 1)}%` })));
      }
      if (a.score && b.score) {
        const d = a.score.mean - b.score.mean;
        const lead = Math.abs(d) < 0.005 ? null : d > 0 ? 'a' : 'b';
        fair.append(h('div', {},
          h('span', { class: 'lead' }, lead ? key(lead, true) : null),
          lead ? `Mean ${label} ahead` : `Mean ${label} level`,
          lead ? h('b', { text: signed(Math.abs(d)) }) : null));
      }
      if (fair.children.length) kids.push(fair);
      else kids.push(h('span'));
    } else {
      kids.push(h('span'));
    }
    if (r) kids.push(side(r, 'r'));
    box.replaceChildren(...kids);
  }

  // ---------- jump points ----------

  function glyph(kind) {
    const k = KINDS[kind];
    const d = k.shape === 'dip' ? 'M1 2h8L5 9z' : 'M5 0.5 9.5 5 5 9.5 0.5 5z';
    return h('svg', { class: `glyph ${k.stream}`, viewBox: '0 0 10 10', 'aria-hidden': 'true' }, h('path', { d }));
  }
  const kindName = (kind) => `${streams[KINDS[kind].stream].label} ${KINDS[kind].verb}`;
  function momentText(mo) {
    const k = KINDS[mo.kind];
    if (k.shape === 'dip') return `${kindName(mo.kind)} to ${fmtScore(k.stream === 'a' ? mo.a : mo.b)}`;
    return `${kindName(mo.kind)} by ${Math.abs(mo.a - mo.b).toFixed(2)}`;
  }
  // the jump points of the pair on screen, with sides swapped if the manifest lists it the other way round
  function moments() {
    if (momentCache) return momentCache;
    const all = S.m.moments || {};
    const a = streams.a;
    const b = streams.b;
    let list = b ? all[`${a.eid}|${b.eid}`] : all[a.eid];
    let swap = false;
    if (!list && b) { list = all[`${b.eid}|${a.eid}`]; swap = true; }
    momentCache = (list || []).map((mo) => {
      const side = swap ? (mo.side === 'a' ? 'b' : 'a') : mo.side;
      return { kind: `${mo.kind}-${side}`, rank: mo.rank, frame: mo.frame, a: scoreAt('a', mo.frame), b: scoreAt('b', mo.frame) };
    }).filter((mo) => KINDS[mo.kind] && streams[KINDS[mo.kind].stream]);
    return momentCache;
  }
  const inFilter = (mo) => S.filter === 'all' || mo.kind === S.filter;

  function renderJump() {
    const all = moments();
    const kinds = Object.keys(KINDS).filter((k) => all.some((mo) => mo.kind === k));
    const chips = $('#jump-filter');
    const chip = (id, ...kids) => h('button', { type: 'button', 'data-filter': id, 'aria-pressed': String(S.filter === id) }, ...kids);
    chips.replaceChildren(chip('all', 'All'), ...kinds.map((k) => chip(k, glyph(k), kindName(k))));
    chips.hidden = kinds.length < 2;
    renderMoments();
  }
  function bindJump() {
    const chips = $('#jump-filter');
    chips.addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (!b) return;
      S.filter = b.dataset.filter;
      for (const c of $$('button', chips)) c.setAttribute('aria-pressed', String(c === b));
      renderMoments();
    });
  }

  function renderMoments() {
    const list = $('#moments');
    const rows = moments().filter(inFilter);
    if (S.filter !== 'all') rows.sort((x, y) => x.rank - y.rank);
    list.replaceChildren(...rows.map((mo) => {
      const k = KINDS[mo.kind];
      const other = k.stream === 'a' ? mo.b : mo.a;
      const mine = k.stream === 'a' ? mo.a : mo.b;
      const vals = k.shape === 'dip'
        ? [h('em', { text: fmtScore(mine) }), other != null ? ` vs ${fmtScore(other)}` : '']
        : [h('em', { text: signed(Math.abs(mo.a - mo.b)) })];
      const btn = h('button', { type: 'button', 'aria-label': `${momentText(mo)}, at ${timecode(mo.frame)}` },
        glyph(mo.kind),
        h('span', { class: 'what', text: kindName(mo.kind) }),
        h('span', { class: 'vals' }, ...vals),
        h('span', { class: 'at', text: timecode(mo.frame).slice(0, 7) }));
      btn.addEventListener('click', () => jumpTo(mo));
      const li = h('li', {}, btn);
      li.dataset.frame = mo.frame;
      li.dataset.kind = mo.kind;
      return li;
    }));
    const empty = $('#moments-empty');
    empty.hidden = rows.length > 0;
    if (!rows.length) {
      empty.textContent = Object.values(streams).some((s) => s.scores)
        ? 'The two encodes never differ enough here to single out a moment.'
        : 'Add per-frame quality logs to list the moments worth a look.';
    }
    for (const p of $$('#pins button')) p.classList.toggle('dim', !inFilter({ kind: p.dataset.kind }));
    painted = -1;
  }

  function jumpTo(mo) {
    if (S.playing) pause();
    seek(mo.frame);
    say(`${momentText(mo)}, at ${timecode(mo.frame)}`);
  }
  function jump(dir) {
    const rows = moments().filter(inFilter).sort((x, y) => x.frame - y.frame);
    // while playing, "previous" means the one just passed, not the one before it
    const slack = S.playing ? Math.round(S.fps) : 0;
    const next = dir > 0
      ? rows.find((mo) => mo.frame > S.frame)
      : rows.reverse().find((mo) => mo.frame < S.frame - slack);
    if (next) jumpTo(next);
    else say(dir > 0 ? 'No later jump point' : 'No earlier jump point');
  }

  function say(text) {
    const node = $('#live');
    node.textContent = '';
    requestAnimationFrame(() => { node.textContent = text; });
  }

  // ---------- other resolutions ----------
  // each is its own export, so nothing of a 4K encode is fetched until it is picked

  function buildVariants() {
    const list = (S.m.variants || []).filter((v) => v.label);
    const box = $('#seg-variant');
    box.hidden = list.length < 2;
    box.replaceChildren(...list.map((v) => (v.href
      ? h('a', { href: v.href, title: `Open the ${v.label} comparison`, text: v.label })
      : h('a', { 'aria-current': 'page', text: v.label }))));
  }

  // ---------- how this was made ----------

  function buildNotes() {
    const n = S.m.notes;
    const model = S.m.metric.model;
    const box = $('#notes');
    const kids = [];
    for (const p of (n && n.body) || []) kids.push(h('p', { text: p }));
    if (model) kids.push(h('p', { text: `Scored per frame with ${S.m.metric.label} (${model}).` }));
    for (const c of (n && n.commands) || []) {
      const id = c.encode === 'reference' ? 'ref' : encodes[c.encode] && encodes[c.encode].id;
      kids.push(h('figure', {}, h('figcaption', {}, id && streams[id] ? key(id, true) : null, c.label), h('pre', { text: c.text })));
    }
    const files = everything().filter((s) => s.src);
    if (files.length) {
      kids.push(h('figure', {}, h('figcaption', { text: 'The files compared' }),
        h('ul', { class: 'files' }, files.map((s) => h('li', {},
          s.id ? key(s.id, true) : null,
          h('a', { href: url(s.src), download: true, text: s.label }),
          s.bytes ? h('span', { text: fmtBytes(s.bytes) }) : null)))));
    }
    const links = ((n && n.links) || []).filter((l) => /^https?:\/\//.test(l.url));
    if (links.length) {
      kids.push(h('ul', {}, links.map((l) => h('li', {}, h('a', { href: l.url, target: '_blank', rel: 'noopener', text: l.label || l.url })))));
    }
    box.replaceChildren(...kids);
    if (n && n.heading) for (const node of $$('.about-h')) node.textContent = n.heading;
    $('#about').hidden = kids.length === 0;
    const first = kids.find((k) => k.tagName === 'P');
    $('#lede').textContent = first ? first.textContent : '';
  }

  // ---------- timeline ----------

  function niceStep(span, target) {
    const raw = span / target;
    const mag = Math.pow(10, Math.floor(Math.log10(raw)));
    for (const k of [1, 2, 2.5, 5, 10]) if (k * mag >= raw) return k * mag;
    return 10 * mag;
  }

  // one polygon per run where `top` stays on one side of `bot`, split at crossings
  function between(xs, top, bot, y) {
    const out = { up: '', down: '' };
    let run = null;
    const pt = (x, v) => x.toFixed(1) + ' ' + y(v).toFixed(1);
    const add = (x, t, b) => { run.t.push(pt(x, t)); run.b.push(pt(x, b)); };
    const close = () => {
      if (run && run.t.length > 1) out[run.sign > 0 ? 'up' : 'down'] += 'M' + run.t.join('L') + 'L' + run.b.reverse().join('L') + 'Z';
      run = null;
    };
    for (let i = 0; i < xs.length; i++) {
      const d = top[i] - bot[i];
      const sign = d > 0 ? 1 : d < 0 ? -1 : run ? run.sign : 1;
      if (run && sign !== run.sign) {
        const pd = top[i - 1] - bot[i - 1];
        const t = pd / (pd - d);
        const xc = xs[i - 1] + (xs[i] - xs[i - 1]) * t;
        const vc = top[i - 1] + (top[i] - top[i - 1]) * t;
        add(xc, vc, vc);
        close();
        run = { sign, t: [], b: [] };
        add(xc, vc, vc);
      }
      if (!run) run = { sign, t: [], b: [] };
      add(xs[i], top[i], bot[i]);
    }
    close();
    return out;
  }

  function drawChart() {
    const box = $('#chart');
    const svg = $('#plot');
    const W = box.clientWidth;
    const H = box.clientHeight;
    if (!W || !S.m) return;
    const a = streams.a && streams.a.scores;
    const b = streams.b && streams.b.scores;
    const both = !!(a && b);
    const label = S.m.metric.label;

    const L = 34;
    const R = 10;
    const pw = Math.max(10, W - L - R);
    const top = 20;
    const stripH = both ? 40 : 0;
    const axisH = 16;
    const plotH = H - top - axisH - (both ? stripH + 18 : 0);
    const stripTop = top + plotH + 18;
    const x = (f) => L + (S.frames > 1 ? (f / (S.frames - 1)) * pw : 0);

    // legend
    const legend = $('#legend');
    const item = (cls, text) => h('span', {}, h('i', { class: cls }), text);
    legend.replaceChildren(
      a ? item('ln a', streams.a.label) : null,
      b ? item('ln b', streams.b.label) : null,
      both ? item('sw a', `${streams.a.label} ahead`) : null,
      both ? item('sw b', `${streams.b.label} ahead`) : null);
    $('#tl-title').textContent = `${label} per frame`;
    box.setAttribute('aria-valuemax', S.frames - 1);

    svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    svg.replaceChildren();
    $('#pins').replaceChildren();
    chart = { L, pw, top, plotH, stripTop, stripH, x, W, H };

    // time axis
    const seconds = (S.frames - 1) / S.fps;
    const tStep = niceTime(seconds, Math.max(2, Math.floor(pw / 90)));
    const base = top + plotH + (both ? stripH + 18 : 0);
    for (let t = 0; t <= seconds + 1e-6; t += tStep) {
      const px = Math.round(x(t * S.fps)) + 0.5;
      svg.append(h('line', { class: 'grid', x1: px, x2: px, y1: base, y2: base + 4 }));
      const anchor = t === 0 ? 'start' : 'middle';
      svg.append(h('text', { x: t === 0 ? L : px, y: base + 14, 'text-anchor': anchor, text: `${Math.floor(t / 60)}:${pad(Math.round(t % 60))}` }));
    }

    if (!a && !b) {
      svg.append(h('line', { class: 'axis', x1: L, x2: L + pw, y1: top + plotH + 0.5, y2: top + plotH + 0.5 }));
      svg.append(h('text', { class: 'cap', x: L, y: top + plotH / 2, text: 'No per-frame quality log was given for these encodes.' }));
      drawHead();
      return;
    }

    // one point per frame, or per pixel column once frames outnumber pixels
    const n = Math.min(a ? a.length : Infinity, b ? b.length : Infinity, S.frames);
    const cols = Math.max(2, Math.min(n, Math.floor(pw)));
    const xs = new Array(cols);
    const va = a ? new Array(cols) : null;
    const vb = b ? new Array(cols) : null;
    for (let c = 0; c < cols; c++) {
      const f0 = cols === n ? c : Math.floor((c * n) / cols);
      const f1 = cols === n ? c + 1 : Math.max(f0 + 1, Math.floor(((c + 1) * n) / cols));
      let sa = 0;
      let sb = 0;
      for (let f = f0; f < f1; f++) { if (a) sa += a[f]; if (b) sb += b[f]; }
      xs[c] = x((f0 + f1 - 1) / 2);
      if (a) va[c] = sa / (f1 - f0);
      if (b) vb[c] = sb / (f1 - f0);
    }

    let lo = Infinity;
    let hi = -Infinity;
    for (const v of [va, vb]) if (v) for (const s of v) { if (s < lo) lo = s; if (s > hi) hi = s; }
    const yMax = hi <= 100 ? 100 : Math.ceil(hi);
    const step = niceStep(Math.max(1, yMax - lo) * 1.15, 3);
    const yMin = Math.max(0, Math.floor((lo - step * 0.1) / step) * step);
    const y = (v) => top + plotH - ((v - yMin) / (yMax - yMin)) * plotH;
    chart.y = y;

    for (let v = yMin; v <= yMax + 1e-9; v += step) {
      const py = Math.round(y(v)) + 0.5;
      svg.append(h('line', { class: v === yMin ? 'axis' : 'grid', x1: L, x2: L + pw, y1: py, y2: py }));
      svg.append(h('text', { x: L - 6, y: py + 3.5, 'text-anchor': 'end', text: String(+v.toFixed(1)) }));
    }

    const loop = h('rect', { class: 'loop', y: top, height: H - top - axisH });
    svg.append(loop);
    chart.loop = loop;

    const path = (v) => 'M' + v.map((s, i) => xs[i].toFixed(1) + ' ' + y(s).toFixed(1)).join('L');
    if (both) {
      const fill = between(xs, va, vb, y);
      svg.append(h('path', { class: 'fa', d: fill.up }), h('path', { class: 'fb', d: fill.down }));
    }
    // B first, so where the curves overlap A's line keeps the same place every time
    if (vb) svg.append(h('path', { class: 'lb', d: path(vb) }));
    if (va) svg.append(h('path', { class: 'la', d: path(va) }));

    if (both) {
      const delta = va.map((v, i) => v - vb[i]);
      const dmax = niceStep(Math.max(0.5, ...delta.map(Math.abs)), 1);
      const mid = stripTop + stripH / 2;
      const yd = (v) => mid - (clamp(v, -dmax, dmax) / dmax) * (stripH / 2);
      const area = between(xs, delta, delta.map(() => 0), yd);
      svg.append(h('path', { class: 'da', d: area.up }), h('path', { class: 'db', d: area.down }));
      svg.append(h('line', { class: 'axis', x1: L, x2: L + pw, y1: Math.round(mid) + 0.5, y2: Math.round(mid) + 0.5 }));
      svg.append(h('text', { x: L - 6, y: stripTop + 7, 'text-anchor': 'end', text: '+' + dmax }));
      svg.append(h('text', { x: L - 6, y: stripTop + stripH, 'text-anchor': 'end', text: '−' + dmax }));
    }

    // pins: one per jump point, in a lane above the curves
    const pins = $('#pins');
    for (const mo of moments()) {
      const pin = h('button', { type: 'button', 'aria-label': `${momentText(mo)}, at ${timecode(mo.frame)}`, title: momentText(mo) }, glyph(mo.kind));
      pin.style.left = x(mo.frame) + 'px';
      pin.dataset.frame = mo.frame;
      pin.dataset.kind = mo.kind;
      pin.classList.toggle('dim', !inFilter(mo));
      pin.addEventListener('pointerdown', (e) => e.stopPropagation());
      pin.addEventListener('click', (e) => { e.stopPropagation(); jumpTo(mo); });
      pins.append(pin);
    }

    chart.cross = h('g', { visibility: 'hidden' },
      h('line', { class: 'cross', y1: top, y2: H - axisH }),
      a ? h('circle', { class: 'dot a', r: 4.5 }) : null,
      b ? h('circle', { class: 'dot b', r: 4.5 }) : null);
    svg.append(chart.cross);
    drawHead();
    drawLoop();
    painted = -1;
  }

  function niceTime(seconds, target) {
    const raw = seconds / target;
    for (const s of [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600]) if (s >= raw) return s;
    return 7200;
  }

  function drawHead() {
    const c = chart;
    c.head = h('g', {},
      h('line', { class: 'head', y1: c.top - 3, y2: c.H - 16 }),
      h('path', { class: 'headcap', d: `M-4.5 ${c.top - 9}h9v4l-4.5 4.5-4.5-4.5z` }));
    $('#plot').append(c.head);
  }
  function drawLoop() {
    if (!chart || !chart.loop) return;
    if (!S.loop) { chart.loop.setAttribute('width', 0); return; }
    chart.loop.setAttribute('x', chart.x(S.loop[0]));
    chart.loop.setAttribute('width', Math.max(1, chart.x(S.loop[1]) - chart.x(S.loop[0])));
  }

  function bindChart() {
    const box = $('#chart');
    const tip = $('#tip');
    let dragging = false;
    const frameAt = (e) => {
      const r = box.getBoundingClientRect();
      return clamp(Math.round(((e.clientX - r.left - chart.L) / chart.pw) * (S.frames - 1)), 0, S.frames - 1);
    };
    const hover = (frame) => {
      S.hover = frame;
      const c = chart;
      if (frame == null || !c.cross) {
        tip.hidden = true;
        if (c.cross) c.cross.setAttribute('visibility', 'hidden');
        return;
      }
      const px = c.x(frame);
      c.cross.setAttribute('visibility', 'visible');
      c.cross.firstChild.setAttribute('x1', px);
      c.cross.firstChild.setAttribute('x2', px);
      const va = scoreAt('a', frame);
      const vb = scoreAt('b', frame);
      const dots = $$('circle', c.cross);
      for (const d of dots) {
        const v = d.classList.contains('a') ? va : vb;
        d.setAttribute('visibility', v == null ? 'hidden' : 'inherit');
        if (v != null) { d.setAttribute('cx', px); d.setAttribute('cy', c.y(v)); }
      }
      const row = (id, v) => (streams[id] && streams[id].scores
        ? h('div', {}, h('i', { class: 'ln ' + id }), h('b', { text: fmtScore(v) }), h('span', { text: streams[id].label })) : null);
      tip.replaceChildren(
        h('header', {}, h('span', { text: timecode(frame) }), h('span', { text: 'f ' + frame })),
        row('a', va), row('b', vb),
        va != null && vb != null
          ? h('div', { class: 'd' }, h('i', { class: 'ln' }), h('b', { text: signed(va - vb) }),
            h('span', { text: Math.abs(va - vb) < 0.005 ? 'level' : `${streams[va > vb ? 'a' : 'b'].label} ahead` }))
          : null);
      tip.hidden = false;
      const tw = tip.offsetWidth;
      tip.style.left = clamp(px + 12 + tw > c.W ? px - 12 - tw : px + 12, 0, c.W - tw) + 'px';
    };

    box.addEventListener('pointerdown', (e) => {
      if (e.button !== 0 || !chart) return;
      dragging = true;
      box.setPointerCapture(e.pointerId);
      if (S.playing) pause();
      seek(frameAt(e));
      hover(frameAt(e));
    });
    box.addEventListener('pointermove', (e) => {
      if (!chart) return;
      const f = frameAt(e);
      if (dragging) seek(f, { quiet: true, soft: true });
      hover(f);
    });
    const up = () => { if (dragging) { dragging = false; seek(S.frame); } };
    box.addEventListener('pointerup', up);
    box.addEventListener('pointercancel', up);
    box.addEventListener('pointerleave', () => { if (!dragging) hover(null); });
    box.addEventListener('focus', () => { if (box.matches(':focus-visible')) hover(S.frame); });
    box.addEventListener('blur', () => hover(null));
    bindChart.hover = hover;
  }

  // ---------- per-frame paint ----------

  function paint() {
    painted = S.frame;
    const f = S.frame;
    $('#tc').textContent = timecode(f);
    $('#fnum').textContent = f;

    const va = scoreAt('a', f);
    const vb = scoreAt('b', f);
    const out = $('#readout');
    const kids = [];
    if (va != null) kids.push(h('span', {}, key('a', true), h('b', { text: fmtScore(va) })));
    if (vb != null) kids.push(h('span', {}, key('b', true), h('b', { text: fmtScore(vb) })));
    if (va != null && vb != null) kids.push(h('span', {}, h('i', { text: 'A − B' }), h('b', { text: signed(va - vb) })));
    out.replaceChildren(...kids);

    if (chart && chart.head) chart.head.setAttribute('transform', `translate(${chart.x(f).toFixed(1)} 0)`);
    const box = $('#chart');
    box.setAttribute('aria-valuenow', f);
    box.setAttribute('aria-valuetext', [
      timecode(f),
      va != null && `${streams.a.label} ${fmtScore(va)}`,
      vb != null && `${streams.b.label} ${fmtScore(vb)}`,
    ].filter(Boolean).join(', '));
    if (S.hover != null && document.activeElement === box && bindChart.hover) bindChart.hover(f);

    // mark the jump point the playhead is sitting in, if any
    const near = Math.max(1, Math.round(S.fps / 4));
    for (const node of [...$$('#moments li'), ...$$('#pins button')]) {
      node.classList.toggle('on', Math.abs(+node.dataset.frame - f) <= (S.playing ? near : 0));
    }
  }

  // ---------- controls ----------

  function buildControls() {
    const solo = $('#seg-solo');
    solo.replaceChildren(...Object.keys(streams).map((id) =>
      h('button', { type: 'button', 'data-solo': id, 'aria-pressed': 'false', text: id === 'ref' ? 'Source' : id.toUpperCase() })));
    solo.addEventListener('click', (e) => { const b = e.target.closest('button'); if (b) setSolo(b.dataset.solo); });

    $('#seg-view').addEventListener('click', (e) => { const b = e.target.closest('button'); if (b) setView(b.dataset.view); });
    $('#seg-zoom').addEventListener('click', (e) => { const b = e.target.closest('button'); if (b) setZoom(+b.dataset.zoom); });
    $('#seg-rate').addEventListener('click', (e) => { const b = e.target.closest('button'); if (b) setRate(+b.dataset.rate); });
    if (S.pair.length < 2) for (const b of $$('#seg-view button')) b.hidden = b.dataset.view !== 'solo';

    $('#btn-flip').addEventListener('click', flip);
    $('#btn-peek').addEventListener('click', () => setPeek(!S.peek));
    $('#btn-theme').addEventListener('click', toggleTheme);
    $('#btn-help').addEventListener('click', () => $('#help').showModal());
    $('#btn-play').addEventListener('click', toggle);
    $('#btn-fprev').addEventListener('click', () => step(-1));
    $('#btn-fnext').addEventListener('click', () => step(1));
    $('#btn-jprev').addEventListener('click', () => jump(-1));
    $('#btn-jnext').addEventListener('click', () => jump(1));
    $('#btn-loop').addEventListener('click', () => setLoop(!S.loop));
    $('#btn-about').addEventListener('click', () => $('#about-dlg').showModal());

    bindJump();
    bindStage();
    bindChart();
    document.addEventListener('keydown', onKey);
    document.addEventListener('keyup', (e) => { if (e.key === 'r' || e.key === 'R') setPeek(false); });
    window.addEventListener('blur', () => setPeek(false));
  }

  function step(n, soft) {
    if (S.playing) pause();
    seek(S.frame + n, { soft });
  }

  function toggleTheme() {
    const root = document.documentElement;
    const now = root.dataset.theme || (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
    const next = now === 'dark' ? 'light' : 'dark';
    root.dataset.theme = next;
    try { localStorage.setItem('veo-theme', next); } catch (e) { /* storage blocked */ }
  }

  function onKey(e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const help = $('#help');
    const open = $('dialog[open]');
    if (open) {
      if (e.key === '?' || e.key === 'i') { open.close(); e.preventDefault(); }
      return;
    }
    const target = e.target instanceof Element ? e.target : document.body;
    if (target.closest('input, textarea, select')) return;
    const onButton = target.closest('button');
    const second = Math.round(S.fps);
    const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    switch (k) {
      case ' ':
        if (onButton) return; // let Space press the focused button
        toggle(); break;
      case 'k': toggle(); break;
      case 'ArrowLeft': step(e.shiftKey ? -second : -1, e.repeat); break;
      case 'ArrowRight': step(e.shiftKey ? second : 1, e.repeat); break;
      case 'Home': step(-S.frames); break;
      case 'End': step(S.frames); break;
      case 'n': case ']': jump(1); break;
      case 'p': case '[': jump(-1); break;
      case 's': setView('split'); break;
      case 'd': setView('side'); break;
      case '1': setSolo('a'); break;
      case '2': setSolo('b'); break;
      case '3': setSolo('ref'); break;
      case 'f': flip(); break;
      case 'r': if (!e.repeat) setPeek(true); break;
      case 'z': setZoom(ZOOMS[(ZOOMS.indexOf(S.zoom) + 1) % ZOOMS.length]); break;
      case '+': case '=': zoomStep(1); break;
      case '-': case '_': zoomStep(-1); break;
      case '0': setZoom(1); break;
      case 'l': setLoop(!S.loop); break;
      case ',': setRate(RATES[Math.max(0, RATES.indexOf(S.rate) - 1)]); break;
      case '.': setRate(RATES[Math.min(RATES.length - 1, RATES.indexOf(S.rate) + 1)]); break;
      case 't': toggleTheme(); break;
      case 'i': if ($('#about').hidden) return; $('#about-dlg').showModal(); break;
      case '?': help.showModal(); break;
      default: return;
    }
    e.preventDefault();
  }

  // ---------- link state ----------
  // the hash holds the frame and the view, so a link opens on the same picture

  function readHash() {
    const q = new URLSearchParams(location.hash.slice(1));
    const num = (name, lo, hi, fallback) => {
      const v = parseFloat(q.get(name));
      return Number.isFinite(v) ? clamp(v, lo, hi) : fallback;
    };
    S.frame = Math.round(num('f', 0, S.frames - 1, 0));
    const a = q.get('a');
    const b = q.get('b');
    if (encodes[a] && encodes[b] && a !== b) { S.sel.a = a; S.sel.b = b; }
    const view = q.get('view');
    if (['split', 'side', 'solo'].includes(view) && (view === 'solo' || S.pair.length > 1)) S.view = view;
    if (streams[q.get('solo')]) S.solo = q.get('solo');
    if (q.get('flip') === '1' && S.pair.length > 1) S.pair.reverse();
    const zoom = num('zoom', 1, 8, 1);
    S.zoom = ZOOMS.includes(zoom) ? zoom : 1;
    S.cx = num('x', 0, 1, 0.5);
    S.cy = num('y', 0, 1, 0.5);
    S.split = num('split', 0, 1, 0.5);
    if (Object.keys(KINDS).includes(q.get('show'))) S.filter = q.get('show');
  }

  let hashTimer = 0;
  function saveHash() {
    clearTimeout(hashTimer);
    hashTimer = setTimeout(() => {
      if (S.playing) return;
      const q = new URLSearchParams();
      if (S.frame) q.set('f', S.frame);
      const def = S.m.compare || {};
      if (S.sel.b && (S.sel.a !== def.a || S.sel.b !== def.b)) { q.set('a', S.sel.a); q.set('b', S.sel.b); }
      if (S.view !== 'split') q.set('view', S.view);
      if (S.view === 'solo' && S.solo !== 'a') q.set('solo', S.solo);
      if (S.pair[0] === 'b') q.set('flip', '1');
      if (S.zoom !== 1) { q.set('zoom', S.zoom); q.set('x', S.cx.toFixed(3)); q.set('y', S.cy.toFixed(3)); }
      if (Math.abs(S.split - 0.5) > 0.005) q.set('split', S.split.toFixed(3));
      const theme = (location.hash.match(/theme=(light|dark)/) || [])[1];
      if (theme) q.set('theme', theme);
      const text = q.toString();
      history.replaceState(null, '', text ? '#' + text : location.pathname + location.search);
    }, 250);
  }

  // for the console and for tests: veoCompare.seek(393)
  window.veoCompare = {
    seek: (frame) => { if (S.playing) pause(); seek(frame); },
    get frame() { return S.frame; },
    get streams() { return Object.values(streams).map((s) => ({ side: s.id, id: s.eid, via: s.via, state: s.state, t0: s.t0, time: s.video.currentTime })); },
  };

  boot();
})();
