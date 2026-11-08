const SHADES = [[255, 224, 160], [255, 190, 140], [255, 129, 96], [255, 107, 53], [244, 114, 152], [236, 72, 120]];
const FIELD_NAMES = ['Sync the pantry','Plan the week','Export the plan','Check page breaks','Print recipe cards','Grocery API client','Build the shopping list','Print styles','Tag filters','Database migrations','Offline cache','Nutrition table','Nightly dependency bump','Upload photos','Dark mode','Share links','Week planner','Unit tests','Import from a URL','Split the recipe page','Refresh auth tokens','Search index','Onboarding copy','Rate limits','Flaky timers','Error pages','Meal prep mode','Run the linter','Cost per serving','Release notes','Favorites','Keyboard navigation','Thumbnail jobs','Review the plan','Local dates'];
const CLAUDE_LINES = ['Read 1 file','● Update(src/search.js)','  ⎿ Added 5 lines','● Bash(npm test)','  ⎿ 96 passed','● Search works.','✻ Crunched for 5s','● Read pantry.js','  ⎿ Read 42 lines','✻ Baked for 8s','● Update(src/print.css)','  ⎿ Added 4 lines','⏵⏵ auto mode on','❯ add search by ingredient'];
const CODEX_LINES = ['>_ OpenAI Codex (v0.167.0)','› Butter in cups fails','• Butter is missing','• Explored','  └ Read convert.js','  + Show details','• Edited src/convert.js (+1 -0)','• Ran npm test','  + 11 lines (^t to expand)','• Working (48s • esc to interrupt)','  └ Tip: Press f3 to search','› Ask Codex to do anything'];
const hash = (n) => { n = Math.imul(n ^ (n >>> 15), 2246822519); n = Math.imul(n ^ (n >>> 13), 3266489917); return (n ^ (n >>> 16)) >>> 0; };
const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
const hero = document.getElementById('hero');

const Field = (() => {
  const CW = 6, CH = 12, LINE = 1.02, FIELD_DIM = .4, FRAME_MS = 17, WORK_MS = 1000, ALIVE_MS = 20000, POINTER_REST_MS = 1400;
  const REVEAL_SPEED = 1.5, GLOW_R = 230, STEP_MS = 16.67, SPRING = .02, DAMP = .82, PUSH = 3.2, LIFT_R = 58, PUSH_R = 120;
  const SCRAMBLE = '#%/+*=&~<>?:'.split('');
  const SHADE_BASE = 1;
  const LUT = [[158, 157, 150], ...SHADES].map((rgb) => Array.from({ length: 17 }, (_, b) => `rgba(${rgb.join(',')},${(b / 16).toFixed(3)})`));
  const cv = document.getElementById('field'), lv = document.getElementById('lights');
  const ctx = cv.getContext('2d'), lctx = lv.getContext('2d');
  let W = 0, H = 0, cols = 0, rows = 0, N = 0, dpr = 1, subdiv = 1, reach = 1;
  let glyph = [], tone, kind, ink, mask, shade, atten, quiet, last;
  let panes = [], flying = [], loose = SHADES.map(() => []), center = { x: 0, y: 0 };
  let cursor = { x: -9999, y: -9999, tx: -9999, ty: -9999, a: 0, ta: 0 };
  let running = false, idleTimer = 0, aliveUntil = 0, lastWork = 0, lastFrame = 0, lastMove = 0, stepBank = 0, revealStart = 0, revealDone = true, frames = 0;

  const looseCount = () => loose.reduce((n, list) => n + list.length, 0);
  const state = () => { let landed = 0, cells = 0, l = Infinity, r = 0, t = Infinity, b = 0; for (let i = 0; i < N; i++) { if (ink[i]) landed++; if (!mask[i]) continue; cells++; const x = (i % cols) * CW, y = Math.floor(i / cols) * CH; l = Math.min(l, x); r = Math.max(r, x + CW); t = Math.min(t, y); b = Math.max(b, y + CH); } return { W, H, frames, running, revealDone, flying: flying.length, loose: looseCount(), cells, landed, box: { l, r, t, b } }; };

  function stamp(x, y, text, t, k) {
    for (let i = 0; i < text.length; i++) {
      const col = x + i;
      if (col < 0 || col >= cols || y < 0 || y >= rows || text[i] === ' ') continue;
      const at = y * cols + col;
      glyph[at] = text[i]; tone[at] = t; kind[at] = k;
    }
  }

  const paneShade = (p) => hash(p.x * 7 + p.y * 13) % SHADES.length;

  function buildPanes() {
    panes = [];
    let x = 0, n = 0;
    const r = (a, b) => a + hash(n++ * 7919 + 13) % (b - a + 1);
    while (x < cols) {
      const w = Math.min(W < 760 ? r(15, 24) : r(24, 42), cols - x);
      let y = -r(0, 8);
      while (y < rows) {
        const h = r(9, 18);
        panes.push({ x, y, w, h, claude: hash(n++ * 31) % 2 === 0, name: FIELD_NAMES[panes.length % FIELD_NAMES.length], typed: 0, line: 0, phase: hash(n++) });
        y += h;
      }
      x += w;
    }
    for (const p of panes) {
      stamp(p.x, p.y, '╭' + '─'.repeat(Math.max(0, p.w - 2)) + '╮', .44, 0);
      stamp(p.x, p.y + p.h - 1, '╰' + '─'.repeat(Math.max(0, p.w - 2)) + '╯', .44, 0);
      for (let yy = p.y + 1; yy < p.y + p.h - 1; yy++) { stamp(p.x, yy, '│', .44, 0); stamp(p.x + p.w - 1, yy, '│', .44, 0); }
      const tint = SHADE_BASE + paneShade(p);
      stamp(p.x + 4, p.y + 1, p.claude ? '✻' : '>_', .95, tint);
      stamp(p.x + (p.claude ? 6 : 7), p.y + 1, p.name.slice(0, p.w - 9), .75, tint);
      const source = p.claude ? CLAUDE_LINES : CODEX_LINES;
      let row = p.y + 3, i = p.phase % source.length;
      while (row < p.y + p.h - 2) {
        if (hash(row * 97 + p.x) % 5 !== 0) stamp(p.x + 2, row, source[i % source.length].slice(0, p.w - 4), .62, 0);
        row += 1 + (hash(row + p.x) % 3 === 0 ? 1 : 0); i++;
      }
      p.bottom = p.y + p.h - 3;
      p.row = p.y + 3;
      p.dot = { x: (p.x + 2) * CW + CW / 2, y: (p.y + 1) * CH + CH / 2 };
    }
  }

  function layoutWord() {
    const lines = W < 760 ? ['Coming', 'soon'] : ['Coming soon'];
    const m = document.createElement('canvas').getContext('2d');
    m.font = '800 100px Mona';
    const em = Math.max(...lines.map((l) => m.measureText(l).width)) / 100;
    const bar = document.querySelector('.bar').getBoundingClientRect().bottom - hero.getBoundingClientRect().top;
    const roomW = Math.min(W - 2 * (W < 760 ? 20 : 48), 1120), roomH = H - bar - 40;
    const F = Math.min(roomW / em, roomH / (lines.length * LINE));
    const height = lines.length * F * LINE;
    const top = bar + (roomH - height) * .44;
    const half = em * F / 2;
    return { lines, F, top, box: { l: W / 2 - half, r: W / 2 + half, t: top + F * .12, b: top + height } };
  }

  function rasterize({ lines, F, top }) {
    subdiv = W < 760 ? 2 : 1;
    const px = CW / subdiv, sw = cols * subdiv, sh = rows * 2 * subdiv;
    const buf = document.createElement('canvas');
    buf.width = sw; buf.height = sh;
    const o = buf.getContext('2d', { willReadFrequently: true });
    o.fillStyle = '#fff'; o.textAlign = 'center'; o.textBaseline = 'alphabetic';
    o.font = `800 ${F / px}px Mona`;
    const lh = F * LINE / px;
    lines.forEach((l, i) => o.fillText(l, sw / 2, top / px + lh * (i + .82)));
    const data = o.getImageData(0, 0, sw, sh).data;
    const out = new Uint8Array(N);
    for (let y = 0; y < rows; y++) for (let x = 0; x < cols; x++) {
      let bits = 0;
      for (let j = 0; j < 2 * subdiv; j++) for (let k = 0; k < subdiv; k++) {
        if (data[((y * 2 * subdiv + j) * sw + x * subdiv + k) * 4 + 3] > 120) bits |= 1 << (j * subdiv + k);
      }
      out[y * cols + x] = bits;
    }
    return out;
  }

  function shadeOfBlock(x, y) {
    const bx = Math.floor(x / 5), by = Math.floor(y / 3), seed = hash(bx * 7919 + by * 104729);
    const along = (x / cols + y / rows * .35) * 1.15 + (seed % 100) / 100 * .4 - .2;
    return Math.max(0, Math.min(SHADES.length - 1, Math.floor(((along % 1) + 1) % 1 * SHADES.length)));
  }

  function buildAtten(word) {
    const r = document.querySelector('.bar').getBoundingClientRect();
    const away = (b, x, y) => Math.hypot(Math.max(b.l - x, 0, x - b.r), Math.max(b.t - y, 0, y - b.b));
    const soft = (d, from, to) => { const t = Math.max(0, Math.min(1, (d - from) / (to - from))); return t * t * (3 - 2 * t); };
    const bar = { l: r.left, r: r.right, t: r.top, b: r.bottom };
    for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) {
      const px = x * CW + CW / 2, py = y * CH + CH / 2;
      quiet[i] = .08 + .92 * soft(away(bar, px, py), 0, 26);
      atten[i] = Math.min(quiet[i], .34 + .66 * soft(away(word.box, px, py), 0, 44)) * Math.max(0, Math.min(1, (H - py) / 48));
    }
  }

  function build() {
    W = hero.clientWidth; H = hero.clientHeight;
    reach = W < 760 ? .7 : 1;
    dpr = Math.min(devicePixelRatio || 1, 2);
    for (const c of [cv, lv]) { c.width = W * dpr; c.height = H * dpr; }
    cols = Math.ceil(W / CW); rows = Math.ceil(H / CH); N = cols * rows;
    glyph = new Array(N).fill(' '); tone = new Float32Array(N); kind = new Uint8Array(N); ink = new Uint8Array(N); shade = new Uint8Array(N); atten = new Float32Array(N); quiet = new Float32Array(N); last = new Int32Array(N).fill(-1);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.font = `${Math.round(CH * .86)}px JBM, Menlo, monospace`;
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'center';
    buildPanes();
    const word = layoutWord();
    mask = rasterize(word);
    for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) shade[i] = shadeOfBlock(x, y);
    buildAtten(word);
    center = { x: W / 2, y: (word.box.t + word.box.b) / 2 };
    flying = [];
    loose = SHADES.map(() => []);
  }

  function gather() {
    const dots = panes.filter((p) => p.dot.y > 0 && p.dot.y < H);
    const cells = [];
    for (let i = 0; i < N; i++) if (glyph[i] !== ' ' && atten[i] > .3) cells.push(i);
    const now = performance.now();
    for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) {
      if (!mask[i]) continue;
      let sx, sy;
      if (hash(i * 23) % 100 < 35) { const from = dots[hash(i * 17) % dots.length]; sx = from.dot.x; sy = from.dot.y; }
      else { const from = cells[hash(i * 29) % cells.length]; sx = (from % cols) * CW + CW / 2; sy = Math.floor(from / cols) * CH + CH / 2; }
      flying.push({ i, sx, sy, tx: x * CW + CW / 2, ty: y * CH + CH / 2, t0: now + Math.hypot(sx - center.x, sy - center.y) / REVEAL_SPEED + hash(i * 3) % 400, dur: 600 + hash(i * 7) % 400 });
    }
    flying.sort((a, b) => shade[a.i] - shade[b.i]);
  }

  function drawLights(now) {
    lctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    lctx.clearRect(0, 0, W, H);
    const reveal = revealDone ? Infinity : (now - revealStart) * REVEAL_SPEED;
    for (const p of panes) {
      if (p.dot.y < -6 || p.dot.y > H + 6) continue;
      const cell = Math.floor(p.dot.y / CH) * cols + Math.floor(p.dot.x / CW);
      const fade = (atten[cell] ?? 0) * (quiet[cell] ?? 0);
      if (fade <= 0 || ink[cell] || Math.hypot(p.dot.x - center.x, p.dot.y - center.y) > reveal) continue;
      lctx.fillStyle = `rgba(${SHADES[paneShade(p)].join(',')},${.85 * FIELD_DIM * fade})`;
      lctx.beginPath(); lctx.arc(p.dot.x, p.dot.y, 2.6, 0, 7); lctx.fill();
    }
  }

  function liftNearPointer() {
    const r = LIFT_R * reach, r2 = r * r;
    const x0 = Math.max(0, Math.floor((cursor.x - r) / CW)), x1 = Math.min(cols - 1, Math.ceil((cursor.x + r) / CW));
    const y0 = Math.max(0, Math.floor((cursor.y - r) / CH)), y1 = Math.min(rows - 1, Math.ceil((cursor.y + r) / CH));
    for (let y = y0; y <= y1; y++) for (let x = x0; x <= x1; x++) {
      const i = y * cols + x;
      if (!ink[i]) continue;
      const hx = x * CW + CW / 2, hy = y * CH + CH / 2, dx = hx - cursor.x, dy = hy - cursor.y, d2 = dx * dx + dy * dy;
      if (d2 > r2) continue;
      ink[i] = 0;
      const d = Math.sqrt(d2) || 1, kick = 1 + hash(i) % 3;
      loose[shade[i]].push({ i, x: hx, y: hy, hx, hy, vx: dx / d * kick, vy: dy / d * kick, swirl: (hash(i * 5) % 100 - 50) / 100 });
    }
  }

  function stepLoose() {
    const r = PUSH_R * reach;
    for (const list of loose) for (let n = list.length - 1; n >= 0; n--) {
      const s = list[n];
      const dx = s.x - cursor.x, dy = s.y - cursor.y, d = Math.hypot(dx, dy) || 1;
      const pushed = cursor.a > .05 && d < r;
      if (pushed) { const f = 1 - d / r, a = PUSH * f * f * cursor.a; s.vx += dx / d * a - dy / d * a * s.swirl; s.vy += dy / d * a + dx / d * a * s.swirl; }
      s.vx = (s.vx + (s.hx - s.x) * SPRING) * DAMP;
      s.vy = (s.vy + (s.hy - s.y) * SPRING) * DAMP;
      s.x += s.vx; s.y += s.vy;
      if (!pushed && Math.hypot(s.hx - s.x, s.hy - s.y) < 1.5 && s.vx * s.vx + s.vy * s.vy < .4) { ink[s.i] = mask[s.i]; list[n] = list[list.length - 1]; list.pop(); }
    }
  }

  function moveSpecks(now, dt) {
    drawLights(now);
    let painted = -1;
    flying = flying.filter((p) => {
      const t = (now - p.t0) / p.dur;
      if (t >= 1) { ink[p.i] = mask[p.i]; return false; }
      if (t < 0) return true;
      if (shade[p.i] !== painted) { lctx.fillStyle = `rgb(${SHADES[shade[p.i]].join(',')})`; painted = shade[p.i]; }
      const eased = t * t * (3 - 2 * t);
      lctx.fillRect(p.sx + (p.tx - p.sx) * eased - 1.5, p.sy + (p.ty - p.sy) * eased - 1.5, 3, 3);
      return true;
    });
    if (cursor.ta > 0 && cursor.a > .05) liftNearPointer();
    for (stepBank += dt; stepBank >= STEP_MS; stepBank -= STEP_MS) stepLoose();
    loose.forEach((list, n) => {
      if (!list.length) return;
      lctx.fillStyle = `rgb(${SHADES[n].join(',')})`;
      for (const s of list) lctx.fillRect(s.x - 1.5, s.y - 1.5, 3, 3);
    });
  }

  function stream() {
    for (let i = 0; i < panes.length; i++) {
      const p = panes[(i * 13 + (lastWork | 0)) % panes.length];
      if (p.dot.y < 0 || p.dot.y > H || (atten[Math.floor(p.dot.y / CH) * cols + Math.floor(p.dot.x / CW)] ?? 0) < .5) continue;
      const source = p.claude ? CLAUDE_LINES : CODEX_LINES;
      const text = source[(p.line + p.phase) % source.length];
      const stepBy = 1 + (hash(p.typed + i + (lastWork | 0)) % 3);
      if (p.row >= 0 && p.row < rows) {
        if (p.typed === 0) for (let k = 0; k < p.w - 4; k++) glyph[p.row * cols + p.x + 2 + k] = ' ';
        for (let k = 0; k < stepBy; k++) {
          const c = text[p.typed + k], cellX = p.x + 2 + p.typed + k;
          if (c === undefined || cellX >= p.x + p.w - 2 || cellX >= cols) break;
          const at = p.row * cols + cellX;
          glyph[at] = c; tone[at] = .5; kind[at] = 0;
        }
      }
      p.typed += stepBy;
      if (p.typed >= text.length || p.typed >= p.w - 4) { p.typed = 0; p.line++; p.row++; if (p.row > p.bottom) p.row = p.y + 3; }
      return;
    }
  }

  function paint(now) {
    cursor.x += (cursor.tx - cursor.x) * .16;
    cursor.y += (cursor.ty - cursor.y) * .16;
    cursor.a += (cursor.ta - cursor.a) * .1;
    if (cursor.ta > 0 && now - lastMove > POINTER_REST_MS) cursor.ta = 0;
    const R2 = GLOW_R * GLOW_R, reveal = revealDone ? Infinity : (now - revealStart) * REVEAL_SPEED, t60 = (now / 70) | 0;
    for (let y = 0, i = 0; y < rows; y++) {
      const py = y * CH + CH / 2;
      for (let x = 0; x < cols; x++, i++) {
        const g = glyph[i], ik = ink[i];
        if (g === ' ' && !ik) { if (last[i] !== -1) { ctx.clearRect(x * CW, y * CH, CW, CH); last[i] = -1; } continue; }
        const px = x * CW + CW / 2;
        let t = ik ? .88 : tone[i] * atten[i] * FIELD_DIM;
        const k = ik ? SHADE_BASE + shade[i] : kind[i];
        let scramble = false;
        if (cursor.a > .004 && !ik) {
          const dx = px - cursor.x, dy = py - cursor.y, d2 = dx * dx + dy * dy;
          if (d2 < R2) {
            const f = 1 - Math.sqrt(d2) / GLOW_R;
            t = Math.max(t, tone[i] * FIELD_DIM * Math.min(1, atten[i] + .4) + f * f * cursor.a * .95 * Math.min(1, tone[i] * 2));
            if (f > .55 && cursor.a > .3 && hash(i + (t60 >> 1)) % 100 < (f - .55) * 120) scramble = true;
          }
        }
        if (reveal !== Infinity) {
          const d = Math.hypot(px - center.x, py - center.y) - reveal;
          if (d > 0) t = 0;
          else if (d > -110) scramble = true;
        }
        if (!ik) { t *= quiet[i]; if (quiet[i] < .5) scramble = false; }
        const b = Math.max(0, Math.min(16, (t * 16 + .5) | 0));
        const shown = scramble ? SCRAMBLE[hash(i + t60) % SCRAMBLE.length] : g;
        const code = scramble ? shown.charCodeAt(0) + 70000 : (ik ? 90000 + ik : g.charCodeAt(0));
        const keyv = code * 4096 + b * 32 + k;
        if (keyv === last[i]) continue;
        last[i] = keyv;
        const cellx = x * CW, celly = y * CH;
        ctx.clearRect(cellx, celly, CW, CH);
        if (b === 0) continue;
        ctx.fillStyle = LUT[k][b];
        if (ik && !scramble) {
          const pp = CW / subdiv, inset = subdiv === 1 ? .5 : .25;
          for (let j = 0; j < 2 * subdiv; j++) for (let q = 0; q < subdiv; q++) {
            if (ik & (1 << (j * subdiv + q))) ctx.fillRect(cellx + q * pp + inset, celly + j * pp + inset, pp - inset * 2, pp - inset * 2);
          }
        } else {
          ctx.fillText(shown, px, py);
        }
      }
    }
    if (!revealDone && reveal > Math.hypot(W, H) + 200) revealDone = true;
  }

  function frame(now) {
    running = false;
    if (document.hidden) return;
    const alive = now < aliveUntil;
    const moving = flying.length > 0 || looseCount() > 0 || cursor.a > .004 || cursor.ta > 0 || !revealDone;
    if (!alive && !moving) return;
    const dueWork = alive && now - lastWork >= WORK_MS - FRAME_MS;
    if (dueWork) { lastWork = now; stream(); }
    const dt = Math.min(now - lastFrame, 67);
    lastFrame = now;
    if (moving) moveSpecks(now, dt);
    if (moving || dueWork) { frames++; paint(now); }
    running = true;
    if (moving) requestAnimationFrame(frame);
    else idleTimer = setTimeout(() => { idleTimer = 0; requestAnimationFrame(frame); }, lastWork + WORK_MS - now);
  }

  function schedule() {
    if (still || document.hidden) return;
    if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; running = false; }
    if (running) return;
    running = true;
    requestAnimationFrame(frame);
  }

  function keepAlive() { aliveUntil = performance.now() + ALIVE_MS; schedule(); }

  function onPointer(e) {
    if (still) return;
    const r = hero.getBoundingClientRect();
    cursor.tx = e.clientX - r.left; cursor.ty = e.clientY - r.top;
    if (cursor.a < .02) { cursor.x = cursor.tx; cursor.y = cursor.ty; }
    cursor.ta = 1; lastMove = performance.now();
    keepAlive();
  }

  function showAtOnce() {
    ink.set(mask);
    revealDone = true;
    const now = performance.now();
    paint(now);
    drawLights(now);
  }

  hero.addEventListener('pointermove', onPointer, { passive: true });
  hero.addEventListener('pointerdown', onPointer, { passive: true });
  hero.addEventListener('pointerleave', () => { cursor.ta = 0; }, { passive: true });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && (flying.length > 0 || looseCount() > 0 || !revealDone || performance.now() < aliveUntil)) schedule();
  });

  let resizeTimer = 0;
  addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => { if (hero.clientWidth === W && hero.clientHeight === H) return; build(); showAtOnce(); }, 160);
  }, { passive: true });

  return {
    start() {
      build();
      if (still) { showAtOnce(); return; }
      revealStart = performance.now();
      revealDone = false;
      gather();
      keepAlive();
    },
    state,
  };
})();

document.fonts.load('800 100px Mona').then(() => document.fonts.load('12px JBM')).finally(() => Field.start());
