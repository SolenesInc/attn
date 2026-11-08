const CLAUDE = 'Claude Code', CODEX = 'Codex';
const add = (n, code) => `<div class="dl add"><u>${n}</u><s>+</s><span>${code}</span></div>`;
const ctx = (n, code) => `<div class="dl"><u>${n}</u><s> </s><span>${code}</span></div>`;
const K = (t) => `<i class="k">${t}</i>`, F = (t) => `<i class="f">${t}</i>`, N = (t) => `<i class="n">${t}</i>`, O = (t) => `<i class="o">${t}</i>`, Y = (t) => `<i class="y">${t}</i>`;
const SP = '<div class="sp"></div>';
const cxBanner = (dir) => `<p class="cxb"><i class="bl">&gt;_</i> <b>OpenAI Codex</b> <i class="dm">(v0.167.0)</i></p><p class="cxp">~/…/attn-demo/${dir}</p>${SP}`;
const AGENTS = [
  { name: 'search-by-ingredient', title: 'Search by ingredient', harness: CLAUDE, cost: '$0.02', reply: 'none, show tips',
    body: `<p class="pl">Read <b>1</b> file</p>${SP}<p class="cm"><b>Update</b>(src/search.js)</p><p class="cr">Added <b>5</b> lines</p>`
      + ctx(1, `${K('import')} { recipes } ${K('from')} ${Y("'./recipes.js'")};`) + ctx(2, '')
      + add(3, `${K('export')} ${F('function')} ${N('byIngredient')}(${O('query')}) {`) + add(4, `  ${F('const')} needle = query.${N('toLowerCase')}();`) + add(5, `  ${K('return')} recipes.${N('filter')}((${O('recipe')}) =&gt; recipe.ingredients.${N('some')}(({ ${O('item')} }) =&gt; item.${N('includes')}(needle)));`) + add(6, '}') + add(7, '')
      + ctx(8, `${K('export')} ${F('function')} ${N('byName')}(${O('query')}) {`) + ctx(9, `  ${F('const')} needle = query.${N('toLowerCase')}();`),
    ask: 'Search works. Should an empty query return every recipe, or none?', worked: 'Crunched for 5s · done 10:01 PM' },
  { name: 'csv-writer', title: 'Write the CSV export', harness: CODEX, cost: '1.2k tokens', role: 'B', reply: 'escape them',
    body: `${cxBanner('recipes--csv-writer')}<p class="band">Write the meal plan to a CSV file</p>${SP}<p class="cb"><b>Explored</b></p><p class="cr2"><i class="bl">Read</i> plan.js</p>${SP}<p class="cb"><b>Edited</b> src/csv.js (<i class="gr">+24</i> <i class="rd">-0</i>)</p><p class="cxdl">1 <s>+</s>  export function toCsv(plan) {</p><p class="cd">+ Show details</p>${SP}<p class="cb on"><b>Ran</b> <i class="bl">npm</i> test</p><p class="cr2">+ 9 lines (^t to expand)</p>`,
    ask: 'Quoted commas break the columns. Escape them, or reject the row?', worked: 'Worked for 1m 12s' },
  { name: 'pdf-layout', title: 'Lay out the PDF', harness: CLAUDE, cost: '$0.01', role: 'B', reply: 'yes, one page',
    body: `<p class="pl">Read <b>2</b> files</p>${SP}<p class="cm"><b>Update</b>(src/print.css)</p><p class="cr">Added <b>4</b> lines</p>`
      + ctx(11, '@media print {') + add(12, `  ${F('.recipe')} {`) + add(13, `    break-inside: ${O('avoid')};`) + add(14, '    margin-bottom: 2rem;') + add(15, '  }') + ctx(16, '}')
      + `${SP}<p class="cm"><b>Bash</b>(npm run pdf)</p><p class="cr">Wrote recipes.pdf, 14 pages</p>`,
    ask: 'Two recipes split across pages. Keep each recipe on one page?', worked: 'Baked for 8s · done 10:03 PM' },
  { name: 'meal-plan-export', title: 'Plan the meal export', harness: CLAUDE, cost: '$0.04', reply: 'yes, quantities',
    body: `<p class="pl">Read <b>2</b> files</p>${SP}<p class="cm"><b>Update</b>(src/export.js)</p><p class="cr">Added <b>4</b> lines</p>`
      + ctx(1, `${K('import')} { toCsv } ${K('from')} ${Y("'./csv.js'")};`) + ctx(2, '')
      + add(3, `${K('export')} ${F('function')} ${N('exportWeek')}(${O('plan')}) {`) + add(4, `  ${K('return')} ${N('toCsv')}(plan.days.${N('flatMap')}((${O('day')}) =&gt; day.meals));`) + add(5, '}') + add(6, '')
      + `${SP}<p class="cm"><b>Bash</b>(npm test)</p><p class="cr">9 passed</p>`,
    ask: 'The CSV export works. Add shopping quantities to it too?', worked: 'Simmered for 12s · done 10:04 PM' },
];
const SIDE = { name: 'fix-unit-conversion', title: 'Fix butter conversion', harness: CODEX, cost: '3.7k tokens', reply: 'yes, commit it',
    body: `${cxBanner('recipes--fix-unit-conversion')}<p class="band">Butter in cups fails to convert to grams, fix it</p>${SP}<p class="cb">Butter is missing from the table.</p>${SP}<p class="cb"><b>Explored</b></p><p class="cr2"><i class="bl">Read</i> convert.js</p>${SP}<p class="cb"><b>Edited</b> src/convert.js (<i class="gr">+1</i> <i class="rd">-0</i>)</p><p class="cxdl">6 <s>+</s>  butter: 227,</p>${SP}<p class="cb on"><b>Ran</b> <i class="bl">npm</i> test</p><p class="cr2">+ 11 lines (^t to expand)</p>`,
    ask: 'Tests pass. Commit the fix?', worked: 'Worked for 48s' };
const HEADLINE = { text: 'Agents scale. Your attention doesn’t.', wide: ['Agents scale.', 'Your attention doesn’t.'], narrow: ['Agents', 'scale. Your', 'attention', 'doesn’t.'] };
const ROW_ORDER = ['Plan the week', 'Plan the meal export', 'Write the CSV export', 'Export the plan', 'Lay out the PDF', 'Search by ingredient'];
const AWAY_AGENTS = 3;
const SHADES = [[255, 224, 160], [255, 190, 140], [255, 129, 96], [255, 107, 53], [244, 114, 152], [236, 72, 120]];
const FIELD_NAMES = ['Sync the pantry','Plan the week','Export the plan','Check page breaks','Print recipe cards','Grocery API client','Build the shopping list','Print styles','Tag filters','Database migrations','Offline cache','Nutrition table','Nightly dependency bump','Upload photos','Dark mode','Share links','Week planner','Unit tests','Import from a URL','Split the recipe page','Refresh auth tokens','Search index','Onboarding copy','Rate limits','Flaky timers','Error pages','Meal prep mode','Run the linter','Cost per serving','Release notes','Favorites','Keyboard navigation','Thumbnail jobs','Review the plan','Local dates'];
const CLAUDE_LINES = ['Read 1 file','● Update(src/search.js)','  ⎿ Added 5 lines','● Bash(npm test)','  ⎿ 96 passed','● Search works.','✻ Crunched for 5s','● Read pantry.js','  ⎿ Read 42 lines','✻ Baked for 8s','● Update(src/print.css)','  ⎿ Added 4 lines','⏵⏵ auto mode on','❯ add search by ingredient'];
const CODEX_LINES = ['>_ OpenAI Codex (v0.167.0)','› Butter in cups fails','• Butter is missing','• Explored','  └ Read convert.js','  + Show details','• Edited src/convert.js (+1 -0)','• Ran npm test','  + 11 lines (^t to expand)','• Working (48s • esc to interrupt)','  └ Tip: Press f3 to search','› Ask Codex to do anything'];
const STILL_MS = 5000, DWELL_MS = 6500, NUDGE_MS = 2500, DISSOLVE_MS = 800, FRAME_MS = 17, ALSO_WAITING_PER_AGENT = 3, ALIVE_MS = 20000, EDGE_SPEED = 3, OUT_SPEED = .8, FRONT = 110, HEAD_FIELD = .42;
const INSTALL = 'brew tap solenesinc/attn https://github.com/SolenesInc/attn\nbrew install --cask solenesinc/attn/attn';
const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
const $ = (id) => document.getElementById(id);
const root = document.documentElement;
const CALM = [
  { tick: 90, panes: 12, flicker: 14, dim: 1 },
  { tick: 140, panes: 7, flicker: 7, dim: .82 },
  { tick: 220, panes: 4, flicker: 3, dim: .68 },
  { tick: 380, panes: 2, flicker: 1, dim: .54 },
  { tick: 1000, panes: 1, flicker: 0, dim: .4 },
];
const hash = (n) => { n = Math.imul(n ^ (n >>> 15), 2246822519); n = Math.imul(n ^ (n >>> 13), 3266489917); return (n ^ (n >>> 16)) >>> 0; };

const Field = (() => {
  const cv = $('field'), lv = $('lights');
  const ctx = cv.getContext('2d'), lctx = lv.getContext('2d');
  const SCRAMBLE = '#%/+*=&~<>?:'.split('');
  const COLORS = { gray: [158, 157, 150], amber: [245, 158, 11], green: [63, 185, 80], white: [241, 239, 233], claude: [217, 119, 87] };
  SHADES.forEach((rgb, n) => { COLORS[`shade${n}`] = rgb; });
  const SHADE_BASE = 5;
  const KINDS = ['gray', 'amber', 'green', 'white', 'claude', ...SHADES.map((_, n) => `shade${n}`)];
  const WAVE_SHADE = SHADE_BASE + 2;
  const LUT = KINDS.map((k) => Array.from({ length: 17 }, (_, b) => `rgba(${COLORS[k].join(',')},${(b / 16).toFixed(3)})`));
  let W = 0, H = 0, cw = 6, ch = 12, cols = 0, rows = 0, N = 0, dpr = 1;
  let glyph = [], tone, kind, ink, atten, quiet, last, cellDim, baseInk, baseKinds, inkShade;
  let panes = [], agentPanes = [], waitingGroups = [];
  let calm = 0, flickerSeed = 0, idleTimer = 0, ending = null, particles = [], onFormed = null;
  let ripples = [], flips = [];
  let cursor = { x: -9999, y: -9999, tx: -9999, ty: -9999, a: 0, ta: 0 };
  let subdiv = 1, revealStart = 0, revealDone = true;
  let running = false, aliveUntil = 0, seen = true, lastWork = 0, frames = 0;
  const state = () => ({ running, frames, panes: panes.length, working: panes.filter((p) => !p.waiting).length, waiting: panes.filter((p) => p.waiting).length, calm, wordmarkCells: inkShade ? inkShade.reduce((n, v) => n + (v > 0), 0) : 0, wordmarkShades: inkShade ? new Set(inkShade.filter(Boolean)).size : 0, cols, rows });

  const LINE = 1.02;
  const headlineWeight = () => (W < 760 ? 640 : 600);
  function headlineLines() { return W < 760 ? HEADLINE.narrow : HEADLINE.wide; }

  const headlineRoom = () => Math.min(W - 2 * (W < 760 ? 12 : 40), 1120);

  function layoutHeadline() {
    const spot = $('spot');
    const m = document.createElement('canvas').getContext('2d');
    const lines = headlineLines();
    m.font = `${headlineWeight()} 100px Mona`;
    m.letterSpacing = '1px';
    const em = Math.max(...lines.map((l) => m.measureText(l).width)) / 100;
    const avail = headlineRoom();
    const budget = (W < 760 ? .29 : .3) * Math.min(H, innerHeight);
    const F = Math.min(avail / em, budget / (lines.length * LINE));
    spot.style.height = Math.ceil(lines.length * F * LINE + F * .14) + 'px';
    return { lines, F };
  }

  function stamp(x, y, text, t, k) {
    for (let i = 0; i < text.length; i++) {
      const col = x + i;
      if (col < 0 || col >= cols || y < 0 || y >= rows) continue;
      const c = text[i];
      if (c === ' ') continue;
      const at = y * cols + col;
      glyph[at] = c; tone[at] = t; kind[at] = k;
    }
  }

  function stampPane(p, name, nameTone) {
    for (let y = p.y + 1; y < p.y + p.h - 1; y++) {
      if (y < 0 || y >= rows) continue;
      for (let x = p.x + 1; x < Math.min(p.x + p.w - 1, cols); x++) glyph[y * cols + x] = ' ';
    }
    stamp(p.x + 4, p.y + 1, p.claude ? '✻' : '>_', .95, p.claude ? 4 : 0);
    stamp(p.x + (p.claude ? 6 : 7), p.y + 1, name.slice(0, p.w - 9), nameTone, 0);
    const source = p.claude ? CLAUDE_LINES : CODEX_LINES;
    let row = p.y + 3, i = p.phase % source.length;
    while (row < p.y + p.h - 2) {
      const text = source[i % source.length].slice(0, p.w - 4);
      if (hash(row * 97 + p.x) % 5 !== 0) stamp(p.x + 2, row, text, .62, 0);
      row += 1 + (hash(row + p.x) % 3 === 0 ? 1 : 0); i++;
    }
  }

  function buildPanes() {
    panes = [];
    let x = 0, n = 0;
    const r = (a, b) => a + hash(n++ * 7919 + 13) % (b - a + 1);
    while (x < cols) {
      const w = Math.min(W < 760 ? r(15, 24) : r(24, 42), cols - x);
      let y = -r(0, 8);
      while (y < rows) {
        const h = r(9, 18);
        const claude = hash(n++ * 31) % 2 === 0;
        panes.push({ x, y, w, h, claude, name: FIELD_NAMES[panes.length % FIELD_NAMES.length], waiting: false, row: y + 2, typed: 0, line: 0, phase: hash(n++) });
        y += h;
      }
      x += w;
    }
    for (const p of panes) {
      const k = 0;
      const b = .44;
      {
        stamp(p.x, p.y, '╭' + '─'.repeat(Math.max(0, p.w - 2)) + '╮', b, k);
        stamp(p.x, p.y + p.h - 1, '╰' + '─'.repeat(Math.max(0, p.w - 2)) + '╯', b, k);
        for (let yy = p.y + 1; yy < p.y + p.h - 1; yy++) { stamp(p.x, yy, '│', b, k); stamp(p.x + p.w - 1, yy, '│', b, k); }
      }
      stampPane(p, p.name, .75);
      p.bottom = p.y + p.h - 3;
      p.row = p.y + 3;
      p.dot = { x: (p.x + 2) * cw + cw / 2, y: (p.y + 1) * ch + ch / 2 };
    }
  }

  function rasterize(draw) {
    const n = W < 760 ? 2 : 1, px = cw / n, sw = cols * n, sh = rows * 2 * n;
    const buf = document.createElement('canvas');
    buf.width = sw; buf.height = sh;
    const o = buf.getContext('2d', { willReadFrequently: true });
    o.fillStyle = '#fff'; o.textAlign = 'center'; o.textBaseline = 'alphabetic';
    draw(o, sw, px);
    const data = o.getImageData(0, 0, sw, sh).data;
    subdiv = n;
    const mask = new Uint8Array(N);
    for (let y = 0; y < rows; y++) for (let x = 0; x < cols; x++) {
      let bits = 0;
      for (let j = 0; j < 2 * n; j++) for (let k = 0; k < n; k++) {
        if (data[((y * 2 * n + j) * sw + x * n + k) * 4 + 3] > 120) bits |= 1 << (j * n + k);
      }
      mask[y * cols + x] = bits;
    }
    return mask;
  }

  function headlineMask(F, lines, top) {
    return rasterize((o, sw, px) => {
      o.font = `${headlineWeight()} ${F / px}px Mona`;
      o.letterSpacing = (F / px * .01) + 'px';
      const lh = F * LINE / px;
      lines.forEach((l, i) => o.fillText(l, sw / 2, top / px + lh * (i + .82)));
    });
  }

  const spotBox = () => { const spot = $('spot').getBoundingClientRect(); return { top: spot.top - $('hero').getBoundingClientRect().top, height: spot.height }; };

  function wordmarkMask() {
    const hero = $('hero').getBoundingClientRect(), win = $('win').getBoundingClientRect(), stage = $('endstage').getBoundingClientRect();
    const top = win.top - hero.top, room = stage.top + document.querySelector('#endstage .cmds').offsetTop - hero.top - 18 - top;
    const m = document.createElement('canvas').getContext('2d');
    m.font = '800 100px Mona';
    const shape = m.measureText('attn');
    const tall = (shape.actualBoundingBoxAscent + shape.actualBoundingBoxDescent) / 100;
    const F = Math.min(win.width * .82 / (shape.width / 100), room * .92 / tall);
    const baseline = top + (room - F * tall) / 2 + F * shape.actualBoundingBoxAscent / 100;
    return rasterize((o, sw, px) => {
      o.font = `800 ${F / px}px Mona`;
      o.fillText('attn', sw / 2, baseline / px);
    });
  }

  function shadeOfBlock(x, y) {
    const bx = Math.floor(x / 5), by = Math.floor(y / 3), seed = hash(bx * 7919 + by * 104729);
    const along = (x / cols + y / rows * .35) * 1.15 + (seed % 100) / 100 * .4 - .2;
    return Math.max(0, Math.min(SHADES.length - 1, Math.floor(((along % 1) + 1) % 1 * SHADES.length)));
  }

  function showWordmarkNow() {
    const mask = wordmarkMask();
    for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) if (mask[i]) { ink[i] = mask[i]; inkShade[i] = shadeOfBlock(x, y) + 1; }
  }

  const paneShade = (p) => hash(p.x * 7 + p.y * 13) % SHADES.length;

  function endKinds() {
    const tinted = baseKinds.slice();
    for (const p of panes) for (let x = p.x + 4; x < Math.min(p.x + p.w - 1, cols); x++) {
      const at = (p.y + 1) * cols + x;
      if (p.y + 1 >= 0 && p.y + 1 < rows && glyph[at] !== ' ') tinted[at] = SHADE_BASE + paneShade(p);
    }
    return tinted;
  }

  function stampHeadline(F, lines) {
    baseInk = headlineMask(F, lines, spotBox().top);
    ink = baseInk.slice();
  }

  function buildAtten() {
    const hero = $('hero').getBoundingClientRect();
    const box = (el) => { const r = el.getBoundingClientRect(); return { l: r.left - hero.left, t: r.top - hero.top, r: r.right - hero.left, b: r.bottom - hero.top }; };
    const sub = box(document.querySelector('.sub')), spot = box($('spot')), bar = box(document.querySelector('.bar'));
    const away = (b, x, y) => Math.hypot(Math.max(b.l - x, 0, x - b.r), Math.max(b.t - y, 0, y - b.b));
    const soft = (d, from, to) => { const t = Math.max(0, Math.min(1, (d - from) / (to - from))); return t * t * (3 - 2 * t); };
    for (let y = 0; y < rows; y++) for (let x = 0; x < cols; x++) {
      const px = x * cw + cw / 2, py = y * ch + ch / 2;
      const clear = Math.min(soft(away(sub, px, py), 12, 44), .08 + .92 * soft(away(bar, px, py), 0, 26));
      const behindHead = HEAD_FIELD + (1 - HEAD_FIELD) * soft(away(spot, px, py), 0, 44);
      quiet[y * cols + x] = clear;
      atten[y * cols + x] = Math.min(clear, behindHead) * Math.max(0, Math.min(1, (H - py) / 48));
    }
  }

  function placeAgents() {
    const hero = $('hero').getBoundingClientRect();
    const win = $('win').getBoundingClientRect();
    const spot = $('spot').getBoundingClientRect(), sub = document.querySelector('.sub').getBoundingClientRect();
    const open = (p) => (quiet[Math.floor(p.dot.y / ch) * cols + Math.floor(p.dot.x / cw)] ?? 0) > .9;
    const clear = (p) => {
      if (!open(p)) return false;
      const sx = p.dot.x + hero.left, sy = p.dot.y + hero.top;
      const inWin = sx > win.left - 14 && sx < win.right + 14 && sy > win.top - 14 && sy < win.bottom + 14;
      const inText = sy > spot.top - 16 && sy < sub.bottom + 16 && sx > Math.min(spot.left, sub.left) - 24 && sx < Math.max(spot.right, sub.right) + 24;
      return !inWin && !inText && p.dot.x > 8 && p.dot.x < W - 8 && p.dot.y > 8 && p.dot.y < H - 40;
    };
    let ok = panes.filter(clear);
    const toWindow = (p) => { const sx = p.dot.x + hero.left, sy = p.dot.y + hero.top; return Math.hypot(Math.max(win.left - sx, 0, sx - win.right), Math.max(win.top - sy, 0, sy - win.bottom)); };
    const nearWindow = ok.filter((p) => toWindow(p) < 190);
    if (nearWindow.length >= AGENTS.length) ok = nearWindow;
    if (ok.length < AGENTS.length) {
      const inked = (p, ry, rx) => { for (let dy = -ry; dy <= ry; dy++) for (let dx = -rx; dx <= rx; dx++) if (ink[(p.y + 1 + dy) * cols + p.x + 2 + dx]) return true; return false; };
      const outsideWin = (p) => { const sy = p.dot.y + hero.top; return sy < win.top - 14 || sy > win.bottom + 14; };
      const usable = panes.filter((p) => open(p) && p.dot.y > 60 && p.dot.y < H - 40 && p.dot.x > 8 && outsideWin(p));
      for (const [ry, rx] of [[3, 7], [1, 2]]) {
        for (const p of usable) if (!ok.includes(p) && !inked(p, ry, rx)) ok.push(p);
        if (ok.length >= AGENTS.length) break;
      }
    }
    const chosen = [];
    const pool = ok.length >= AGENTS.length ? ok : panes;
    pool.sort((a, b) => (hash(a.x * 3 + a.y) - hash(b.x * 3 + b.y)));
    chosen.push(pool[0]);
    while (chosen.length < AGENTS.length && chosen.length < pool.length) {
      let best = null, bestD = -1;
      for (const p of pool) {
        if (chosen.includes(p)) continue;
        const d = Math.min(...chosen.map((c) => Math.hypot(c.dot.x - p.dot.x, c.dot.y - p.dot.y)));
        if (d > bestD) { bestD = d; best = p; }
      }
      chosen.push(best);
    }
    agentPanes = chosen;
    agentPanes.forEach((p, i) => { p.claude = AGENTS[i].harness === CLAUDE; stampPane(p, AGENTS[i].title, 1); });
    const alsoWaiting = panes.filter((p) => clear(p) && !agentPanes.includes(p)).sort((a, b) => hash(a.x * 5 + a.y) - hash(b.x * 5 + b.y)).slice(0, AGENTS.length * ALSO_WAITING_PER_AGENT);
    waitingGroups = agentPanes.map((p) => [p]);
    alsoWaiting.forEach((p, i) => waitingGroups[i % waitingGroups.length].push(p));
  }

  function drawLights() {
    lctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    lctx.clearRect(0, 0, W, H);
    const reveal = revealRadius(performance.now());
    for (const p of panes) {
      if (p.dot.y < -6 || p.dot.y > H + 6) continue;
      const cell = Math.floor(p.dot.y / ch) * cols + Math.floor(p.dot.x / cw);
      const fade = (p.waiting ? Math.max(0, Math.min(1, (H - p.dot.y) / 140)) : (atten[cell] ?? 0)) * (quiet[cell] ?? 0);
      if (fade <= 0) continue;
      if (!revealDone && Math.hypot(p.dot.x - W / 2, p.dot.y - H * .3) > reveal) continue;
      if (ink[(p.y + 1) * cols + p.x + 2]) continue;
      const c = p.waiting ? COLORS.amber : performance.now() >= (p.tintAt ?? Infinity) ? SHADES[paneShade(p)] : COLORS.green;
      if (p.waiting) {
        const g = lctx.createRadialGradient(p.dot.x, p.dot.y, 0, p.dot.x, p.dot.y, 34);
        g.addColorStop(0, `rgba(${c.join(',')},.55)`); g.addColorStop(1, `rgba(${c.join(',')},0)`);
        lctx.fillStyle = g; lctx.fillRect(p.dot.x - 34, p.dot.y - 34, 68, 68);
      }
      lctx.fillStyle = `rgba(${c.join(',')},${(p.waiting ? 1 : .85 * CALM[calm].dim) * fade})`;
      lctx.beginPath(); lctx.arc(p.dot.x, p.dot.y, p.waiting ? 4.5 : 2.6, 0, 7); lctx.fill();
    }
  }

  function build() {
    const hero = $('hero');
    W = hero.clientWidth; H = hero.clientHeight;
    const { F, lines } = layoutHeadline();
    H = hero.clientHeight;
    cw = 6; ch = 12;
    dpr = Math.min(devicePixelRatio || 1, 2);
    for (const c of [cv, lv]) { c.width = W * dpr; c.height = H * dpr; }
    cols = Math.ceil(W / cw); rows = Math.ceil(H / ch); N = cols * rows;
    glyph = new Array(N).fill(' '); tone = new Float32Array(N); kind = new Uint8Array(N); inkShade = new Uint8Array(N); atten = new Float32Array(N); quiet = new Float32Array(N); last = new Int32Array(N).fill(-1); cellDim = new Float32Array(N).fill(CALM[calm].dim);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);
    ctx.font = `${Math.round(ch * .86)}px JBM, Menlo, monospace`;
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'center';
    buildPanes();
    stampHeadline(F, lines);
    buildAtten();
    placeAgents();
    baseKinds = kind.slice();
    syncAgents();
    particles = [];
    if (ending?.wordmark) { showWordmarkNow(); kind = endKinds(); panes.forEach((p) => { p.tintAt = 0; }); finishForming(); }
  }

  function finishForming() {
    if (!onFormed) return;
    const formed = onFormed;
    onFormed = null;
    formed();
  }

  function syncAgents() {
    flips.forEach(clearTimeout); flips = [];
    panes.forEach((p) => { p.waiting = false; });
    AGENTS.forEach((a, i) => { for (const p of waitingGroups[i] ?? []) p.waiting = !!a.waitingNow; });
    drawLights();
  }

  const revealRadius = (now) => (now - revealStart) * 1.5;

  function frame(now) {
    running = false;
    if (!seen) return;
    const alive = now < aliveUntil;
    const moving = ripples.length > 0 || particles.length > 0 || cursor.a > .004 || cursor.ta > 0 || !revealDone;
    if (!alive && !moving) return;
    const dueWork = alive && now - lastWork >= CALM[calm].tick - FRAME_MS;
    if (dueWork) { lastWork = now; stream(); }
    if (particles.length) flyParticles(now);
    if (moving || dueWork) { frames++; paint(now); }
    running = true;
    if (moving) requestAnimationFrame(frame);
    else idleTimer = setTimeout(() => { idleTimer = 0; requestAnimationFrame(frame); }, lastWork + CALM[calm].tick - now);
  }

  function flyParticles(now) {
    drawLights();
    let painted = -1;
    particles = particles.filter((p) => {
      const t = (now - p.t0) / p.dur;
      if (t >= 1) { ink[p.i] = p.mask; inkShade[p.i] = p.shade + 1; return false; }
      if (t < 0) return true;
      if (p.shade !== painted) { lctx.fillStyle = `rgb(${SHADES[p.shade].join(',')})`; painted = p.shade; }
      const eased = t * t * (3 - 2 * t);
      lctx.fillRect(p.sx + (p.tx - p.sx) * eased - 1.5, p.sy + (p.ty - p.sy) * eased - 1.5, 3, 3);
      return true;
    });
    if (!particles.length) { drawLights(); finishForming(); }
  }

  function stream() {
    let n = 0;
    flickerSeed++;
    for (let i = 0; i < panes.length && n < CALM[calm].panes; i++) {
      const p = panes[(i * 13 + (lastWork | 0)) % panes.length];
      if (p.waiting) continue;
      if (p.dot.y < 0 || p.dot.y > H) continue;
      const source = p.claude ? CLAUDE_LINES : CODEX_LINES;
      const text = source[(p.line + p.phase) % source.length];
      const stepBy = 1 + (hash(p.typed + i + (lastWork | 0)) % 3);
      if (p.row >= 0 && p.row < rows) {
        if (p.typed === 0) for (let k = 0; k < p.w - 4; k++) glyph[p.row * cols + p.x + 2 + k] = ' ';
        for (let k = 0; k < stepBy; k++) {
          const c = text[p.typed + k];
          const cellX = p.x + 2 + p.typed + k;
          if (c === undefined || cellX >= p.x + p.w - 2 || cellX >= cols) break;
          const at = p.row * cols + cellX;
          glyph[at] = c; tone[at] = .5; kind[at] = 0;
        }
      }
      p.typed += stepBy;
      if (p.typed >= text.length || p.typed >= p.w - 4) { p.typed = 0; p.line++; p.row++; if (p.row > p.bottom) p.row = p.y + 3; }
      n++;
    }
  }

  function paint(now) {
    cursor.x += (cursor.tx - cursor.x) * .16;
    cursor.y += (cursor.ty - cursor.y) * .16;
    cursor.a += (cursor.ta - cursor.a) * .1;
    if (cursor.ta > 0 && now - lastMove > 1400) cursor.ta = 0;
    const R = 230, R2 = R * R;
    const reveal = revealDone ? Infinity : revealRadius(now);
    const cx0 = W / 2, cy0 = H * .3;
    ripples = ripples.filter((r) => now - r.t0 < r.dur);
    const live = ripples.map((r) => { const age = now - r.t0; return { arr: r.arr, age, v: r.v, w: r.w, s: r.s * (1 - r.fade * age / r.dur), k: r.k, dim: r.dim, ink: r.ink, kinds: r.kinds }; });
    const t60 = (now / 70) | 0;
    const flicker = still ? 0 : CALM[calm].flicker;
    const washes = live.filter((r) => r.dim !== undefined);
    let drawn = 0;
    for (let y = 0, i = 0; y < rows; y++) {
      const py = y * ch + ch / 2;
      for (let x = 0; x < cols; x++, i++) {
        for (const r of washes) if (r.age >= r.arr[i] || r.arr[i] === Infinity) { cellDim[i] = r.dim; if (r.ink) { ink[i] = r.ink[i]; inkShade[i] = 0; } if (r.kinds) kind[i] = r.kinds[i]; }
        const g = glyph[i], ik = ink[i];
        if (g === ' ' && !ik) { if (last[i] !== -1) { ctx.clearRect(x * cw, y * ch, cw, ch); last[i] = -1; } continue; }
        const px = x * cw + cw / 2;
        let t = ik ? (inkShade[i] ? .88 : 1) : tone[i] * atten[i] * cellDim[i];
        let k = ik ? (inkShade[i] ? SHADE_BASE + inkShade[i] - 1 : 3) : kind[i];
        let scramble = false;
        if (flicker && !ik && atten[i] > .6 && hash(i * 31 + flickerSeed) % 1000 < flicker) { scramble = true; t = Math.min(1, t * 1.6); }
        if (cursor.a > .004) {
          const dx = px - cursor.x, dy = py - cursor.y, d2 = dx * dx + dy * dy;
          if (d2 < R2) {
            const f = 1 - Math.sqrt(d2) / R;
            if (ik) { if (f > .72 && cursor.a > .3 && (hash(i + (t60 >> 1)) % 100) < (f - .72) * 170) scramble = true; }
            else t = Math.max(t, tone[i] * cellDim[i] * Math.min(1, atten[i] + .4) + f * f * cursor.a * .95 * Math.min(1, tone[i] * 2));
          }
        }
        for (const r of live) {
          const d = (r.age - r.arr[i]) * r.v;
          if (d > -r.w && d < r.w * .25) {
            const a = (1 - Math.abs(d) / r.w) * r.s;
            if (ik) { if (a > .28) { scramble = true; } }
            else { t = Math.max(t, a); k = r.k; if (a > .4) scramble = true; }
          }
        }
        if (reveal !== Infinity) {
          const d = Math.hypot(px - cx0, py - cy0) - reveal;
          if (d > 0) t = 0;
          else if (d > -110) scramble = true;
        }
        if (!ik) { t *= quiet[i]; if (quiet[i] < .5) scramble = false; }
        const b = Math.max(0, Math.min(16, (t * 16 + .5) | 0));
        const ch0 = scramble ? SCRAMBLE[hash(i + t60) % SCRAMBLE.length] : g;
        const code = scramble ? ch0.charCodeAt(0) + 70000 : (ik ? 90000 + ik : g.charCodeAt(0));
        const keyv = code * 4096 + b * 32 + k;
        if (keyv === last[i]) continue;
        last[i] = keyv;
        const cellx = x * cw, celly = y * ch;
        ctx.clearRect(cellx, celly, cw, ch);
        drawn++;
        if (b === 0) continue;
        ctx.fillStyle = LUT[k][b];
        if (ik && !scramble) {
          const pp = cw / subdiv, inset = subdiv === 1 ? .5 : .25;
          for (let j = 0; j < 2 * subdiv; j++) for (let q = 0; q < subdiv; q++) {
            if (ik & (1 << (j * subdiv + q))) ctx.fillRect(cellx + q * pp + inset, celly + j * pp + inset, pp - inset * 2, pp - inset * 2);
          }
        } else {
          ctx.fillText(ch0, px, py);
        }
      }
    }
    if (!revealDone) {
      drawLights();
      if (revealRadius(now) > Math.hypot(W, H) + 200) { revealDone = true; last.fill(-1); ctx.clearRect(0, 0, W, H); paint(now); drawLights(); }
    }
    return drawn;
  }

  function schedule() {
    if (still || !seen) return;
    if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; running = false; }
    if (running) return;
    running = true;
    requestAnimationFrame(frame);
  }
  function keepAlive() { aliveUntil = performance.now() + ALIVE_MS; schedule(); }
  function ring(p, k, s, reach) {
    if (still || !p) return;
    const v = .95, arr = new Float32Array(N);
    for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) arr[i] = Math.hypot(x * cw + cw / 2 - p.dot.x, y * ch + ch / 2 - p.dot.y) / v;
    ripples.push({ arr, t0: performance.now(), dur: reach / v, v, w: 120, s, k, fade: 1 });
    keepAlive();
  }

  function washFromWindow(origin, k, s, dim, changes) {
    const hero = $('hero').getBoundingClientRect(), win = $('win').getBoundingClientRect();
    const L = win.left - hero.left, T = win.top - hero.top, R = win.right - hero.left, B = win.bottom - hero.top;
    const ww = R - L, wh = B - T, P = 2 * (ww + wh);
    const along = (x, y) => {
      const dl = x - L, dr = R - x, dt = y - T, db = B - y, m = Math.min(dl, dr, dt, db);
      return m === dt ? dl : m === dr ? ww + dt : m === db ? ww + wh + dr : 2 * ww + wh + db;
    };
    const from = along(origin.x - hero.left, origin.y - hero.top);
    const arr = new Float32Array(N);
    let latest = 0;
    for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) {
      const px = x * cw + cw / 2, py = y * ch + ch / 2;
      if (px > L && px < R && py > T && py < B) { arr[i] = Infinity; continue; }
      const qx = Math.max(L, Math.min(R, px)), qy = Math.max(T, Math.min(B, py));
      const gap = Math.abs(along(qx, qy) - from);
      arr[i] = Math.min(gap, P - gap) / EDGE_SPEED + Math.hypot(px - qx, py - qy) / OUT_SPEED;
      if (arr[i] > latest) latest = arr[i];
    }
    ripples.push({ arr, t0: performance.now(), dur: latest + FRONT / OUT_SPEED, v: OUT_SPEED, w: FRONT, s, k, fade: .55, dim, ...changes });
    runEdge(from, L, T, ww, wh, P, COLORS[KINDS[k]].join(','));
    keepAlive();
    return arr;
  }

  function flipWhenReached(arr, p, waiting, withRing) {
    const reached = arr[Math.floor(p.dot.y / ch) * cols + Math.floor(p.dot.x / cw)];
    flips.push(setTimeout(() => { p.waiting = waiting; drawLights(); if (withRing) ring(p, 2, .8, 220); }, Number.isFinite(reached) ? reached : 400));
  }

  function calmNow() { if (!N) return; cellDim.fill(CALM[calm].dim); paint(performance.now()); }

  function runEdge(from, L, T, ww, wh, P, rgb) {
    const svg = $('edge');
    svg.style.cssText = `left:${L}px;top:${T}px;width:${ww}px;height:${wh}px`;
    svg.setAttribute('viewBox', `0 0 ${ww} ${wh}`);
    const duration = P / 2 / EDGE_SPEED, tail = 180;
    for (const dir of [1, -1]) for (const [width, alpha] of [[12, .28], [3, 1]]) {
      const r = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
      for (const [a, v] of Object.entries({ width: ww, height: wh, rx: 12, fill: 'none', 'stroke-width': width, 'stroke-linecap': 'round', stroke: `rgba(${rgb},${alpha})`, pathLength: P, 'stroke-dasharray': `${tail} ${P - tail}` })) r.setAttribute(a, v);
      const start = dir > 0 ? from - tail : from;
      svg.append(r);
      r.animate([{ strokeDashoffset: -start, opacity: 1 }, { strokeDashoffset: -(start + dir * P / 2), opacity: .15 }], { duration }).onfinish = () => r.remove();
    }
  }

  let lastMove = 0;
  function onMove(e) {
    if (still) return;
    const r = $('hero').getBoundingClientRect();
    cursor.tx = e.clientX - r.left; cursor.ty = e.clientY - r.top;
    if (cursor.a < .02) { cursor.x = cursor.tx; cursor.y = cursor.ty; }
    cursor.ta = 1; lastMove = performance.now();
    keepAlive();
  }
  $('hero').addEventListener('pointermove', onMove, { passive: true });
  $('hero').addEventListener('pointerleave', () => { cursor.ta = 0; }, { passive: true });

  function setSeen(v) {
    seen = v && !document.hidden;
    if (seen && performance.now() < aliveUntil) schedule();
    if (seen && (!revealDone || particles.length)) schedule();
  }
  new IntersectionObserver(([e]) => setSeen(e.isIntersecting)).observe($('hero'));
  document.addEventListener('visibilitychange', () => setSeen(!document.hidden));

  let resizeTimer = 0;
  addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => { revealDone = true; build(); paint(performance.now()); drawLights(); }, 160);
  }, { passive: true });

  return {
    start() {
      build();
      if (still) { revealDone = true; paint(performance.now()); drawLights(); return; }
      revealStart = performance.now();
      revealDone = false;
      keepAlive();
    },
    settle(i, origin, level) {
      calm = level;
      const last = level === CALM.length - 1;
      if (still) { if (last) { kind = endKinds(); panes.forEach((p) => { p.tintAt = 0; }); } calmNow(); (waitingGroups[i] ?? []).forEach((p) => { p.waiting = false; }); drawLights(); return; }
      const arr = washFromWindow(origin, last ? WAVE_SHADE : 2, .9, CALM[level].dim, last ? { kinds: endKinds() } : {});
      if (last) { const now = performance.now(); for (const p of panes) { const reached = arr[Math.floor(p.dot.y / ch) * cols + Math.floor(p.dot.x / cw)]; p.tintAt = now + (Number.isFinite(reached) ? reached : 0); } }
      (waitingGroups[i] ?? []).forEach((p, n) => flipWhenReached(arr, p, false, n === 0));
    },
    gather(leave, formed) {
      ending = { wordmark: true };
      onFormed = formed;
      if (still) { showWordmarkNow(); calmNow(); drawLights(); finishForming(); return; }
      const mask = wordmarkMask();
      const fromPanes = panes.filter((p) => p.dot.y > 0 && p.dot.y < H);
      const now = performance.now();
      for (let y = 0, i = 0; y < rows; y++) for (let x = 0; x < cols; x++, i++) {
        if (!mask[i]) continue;
        const tx = x * cw + cw / 2, ty = y * ch + ch / 2, dur = 600 + hash(i * 7) % 400;
        let sx, sy, t0;
        if (hash(i * 23) % 100 < 65) {
          const from = hash(i * 29) % leave.at.length;
          sx = leave.left + (from % leave.across + .5) * leave.cell;
          sy = leave.top + (Math.floor(from / leave.across) + .5) * leave.cell;
          t0 = now + leave.at[from] * DISSOLVE_MS;
        } else {
          const from = fromPanes[hash(i * 17) % fromPanes.length];
          sx = from.dot.x; sy = from.dot.y;
          t0 = now + hash(i * 3) % DISSOLVE_MS;
        }
        particles.push({ i, mask: mask[i], shade: shadeOfBlock(x, y), sx, sy, tx, ty, t0, dur });
      }
      particles.sort((a, b) => a.shade - b.shade);
      keepAlive();
    },
    restart(origin) {
      calm = 0;
      ending = null;
      panes.forEach((p) => { p.tintAt = Infinity; });
      if (still) { ink = baseInk.slice(); kind = baseKinds.slice(); inkShade.fill(0); calmNow(); syncAgents(); return; }
      ripples = [];
      particles = [];
      onFormed = null;
      flips.forEach(clearTimeout); flips = [];
      const arr = washFromWindow(origin, 1, .7, CALM[0].dim, { ink: baseInk, kinds: baseKinds });
      AGENTS.forEach((a, i) => (waitingGroups[i] ?? []).forEach((p) => flipWhenReached(arr, p, true, false)));
    },
    ping(i) { ring(agentPanes[i], 1, .5, 420); },
    show() {
      build();
      if (still) { revealDone = true; paint(performance.now()); drawLights(); return; }
      revealStart = performance.now(); revealDone = false; keepAlive();
    },
    subInk() {
      const r = document.querySelector('.sub').getBoundingClientRect(), h = $('hero').getBoundingClientRect();
      const sample = (c) => { const d = c.getContext('2d').getImageData((r.left - h.left) * dpr, (r.top - h.top) * dpr, r.width * dpr, r.height * dpr).data; let m = 0; for (let i = 3; i < d.length; i += 4) if (d[i] > m) m = d[i]; return m; };
      return { field: sample(cv), lights: sample(lv) };
    },
    state,
  };
})();

let queue, working, on, view = 'agent';
let auto = !still, paneSeen = true, stillTimer = 0, typeTimer = 0, sending = false, steps = [];
const main = document.querySelector('.main'), rowsEl = $('rows'), keywrap = $('keywrap'), key = $('key'), copy = $('copy');
const phone = matchMedia('(max-width: 760px)');

const logoUse = (a) => `<svg class="logo" aria-hidden="true"><use href="#i-${a.harness === CLAUDE ? 'claude' : 'codex'}"/></svg>`;
const rowFor = new Map(AGENTS.map((a) => {
  const li = document.createElement('li');
  const b = document.createElement('button');
  b.type = 'button';
  b.innerHTML = `<span class="ic">${logoUse(a)}<sub>1</sub></span><span class="name"></span>${a.role ? `<span class="role">${a.role}</span>` : ''}<span class="age">now</span>`;
  b.querySelector('.name').textContent = a.title;
  b.setAttribute('aria-label', a.title);
  b.addEventListener('click', () => jumpTo(a));
  b.addEventListener('pointerenter', () => { if (queue.includes(a) && a !== on) Field.ping(AGENTS.indexOf(a)); });
  li.append(b);
  return [a, li];
}));

function reset() {
  steps.forEach(clearTimeout); steps = []; sending = false; root.classList.remove('sending');
  queue = [...AGENTS];
  working = [];
  AGENTS.forEach((a) => { a.waitingNow = true; });
  on = queue[0]; view = 'agent';
}

function chromeFor(a, opts) {
  if (a.harness === CLAUDE) {
    const asked = `${SP}<p class="cm w">${a.ask}</p>${SP}<p class="wk">${a.worked}</p>`;
    const talk = opts.work ? `${opts.sent ? `${asked}${SP}<p class="you">${a.reply}</p>` : ''}${SP}<p class="spin">✶ Crunching… (esc to interrupt)</p>` : asked;
    const caret = opts.work ? '' : '<span class="typed"></span><i class="caret"></i>';
    return { talk: `${talk}<div class="cin">${caret}</div><p class="chint"><em>⏵⏵ auto mode on</em> (shift+tab to cycle)</p>`, pin: '' };
  }
  const asked = `${SP}<p class="cb">${a.ask}</p>${SP}<p class="cxrule">${a.worked}</p>`;
  const talk = opts.work
    ? `${opts.sent ? `${asked}${SP}<p class="band">${a.reply}</p>` : ''}${SP}<p class="cb" style="color:#8d8d8d">Working (48s • <b>esc</b> to interrupt)</p><p class="cr2"><i class="tip">Tip:</i> Press <i class="gr">f3</i> to search this conversation.</p>`
    : asked;
  const input = opts.work ? '<i class="caret"></i><span class="ph">Ask Codex to do anything</span>' : '<span class="typed"></span><i class="caret"></i><span class="ph">Ask Codex to do anything</span>';
  return { talk, pin: `<p class="band cinb">${input}</p><p class="cst"><i class="tan">gpt-6.1-sol default · ~/projects/attn-demo/recip…</i></p><p class="cst">← for agents · ? for shortcuts</p>` };
}

function renderTerm(el, a, opts) {
  const { talk, pin } = chromeFor(a, opts);
  el.innerHTML = `<div class="tx">${a.body}${talk}</div><div class="fillr"></div><div class="pin">${pin}</div>`;
}

function placeKey() {
  const slot = phone.matches ? $('win') : document.querySelector('#f-term .cin, #f-term .cinb');
  if (slot && keywrap.parentElement !== slot) slot.append(keywrap);
}
phone.addEventListener('change', placeKey);

function typeReply(a) {
  clearInterval(typeTimer);
  const span = document.querySelector('#f-term .typed');
  if (!span) return;
  if (still) { span.textContent = a.reply; span.parentElement.querySelector('.ph')?.remove(); return; }
  let n = 0;
  typeTimer = setInterval(() => {
    n += 1;
    span.textContent = a.reply.slice(0, n);
    span.parentElement.querySelector('.ph')?.remove();
    if (n >= a.reply.length) clearInterval(typeTimer);
  }, 32);
}

function render(opts = {}) {
  const home = on === null || view === 'home';
  root.classList.toggle('at-home', home);
  root.classList.toggle('waitingstill', queue.length > 0);
  const n = queue.length;
  document.title = n ? `(${n}) attn` : 'attn';
  $('count').className = n ? 'count' : 'count none';
  $('count').innerHTML = `<span><b>${n}</b> waiting</span><i class="rule"></i><kbd>⌘J</kbd>`;
  const others = working.length + AWAY_AGENTS;
  $('more').innerHTML = `▸ ${others} MORE AGENTS<i class="rule"></i><kbd>⌘⇧A</kbd>`;
  $('nworking').innerHTML = `<b>${others}</b> working`;
  $('dwait').hidden = !n;
  $('state').textContent = n ? `${n} still waiting` : 'All caught up';
  $('again').textContent = n ? 'Back to the agents' : 'Run again';
  $('status').textContent = home ? 'Home.' : `${on.title} is waiting for you. ${n} waiting.`;
  $('homebtn').classList.toggle('on', home);
  rowsEl.replaceChildren(...queue.map((a) => {
    const li = rowFor.get(a);
    li.className = '';
    li.firstChild.classList.toggle('on', !home && a === on);
    return li;
  }));
  $('front').classList.remove('done');
  if (on) {
    $('f-name').textContent = on.title; $('f-h').textContent = on.cost;
    renderTerm($('f-term'), on, { work: false });
    placeKey();
    if (!home && opts.type !== false) typeReply(on);
  }
  $('s-name').textContent = SIDE.title; $('s-h').textContent = SIDE.cost;
  renderTerm($('s-term'), SIDE, { work: true });
  renderShelf();
}

function renderShelf() {
  const onScreen = [SIDE.title, on?.title];
  const chips = ROW_ORDER.filter((title) => !onScreen.includes(title)).map((title) => {
    const c = document.createElement('span');
    c.className = queue.some((a) => a.title === title) ? 'chip needs' : 'chip';
    c.dataset.title = title;
    c.innerHTML = '<i></i><b></b>';
    c.lastChild.textContent = title;
    return c;
  });
  const dots = document.createElement('span');
  dots.className = 'tiledots';
  dots.innerHTML = '<span><i class="busy"></i></span>' + (on ? '<span class="cur"><i class="needs"></i></span>' : '');
  $('shelf').replaceChildren(...chips, dots);
}

function settleOn() {
  const position = queue.indexOf(on);
  queue.splice(position, 1);
  on.waitingNow = false;
  working.push(on);
  on = queue[position] ?? queue[0] ?? null;
}

const later = (ms, fn) => steps.push(setTimeout(fn, ms));

function lightOrigin() {
  const d = $('front').querySelector('.light').getBoundingClientRect();
  return { x: d.left + d.width / 2, y: d.top + d.height / 2 };
}

function flyToShelf(tile, title) {
  const from = tile.getBoundingClientRect();
  const ghost = tile.cloneNode(true);
  ghost.removeAttribute('id');
  ghost.querySelectorAll('[id]').forEach((e) => e.removeAttribute('id'));
  ghost.classList.add('ghost');
  Object.assign(ghost.style, { left: from.left + 'px', top: from.top + 'px', width: from.width + 'px', height: from.height + 'px' });
  document.body.append(ghost);
  return () => {
    const chip = [...document.querySelectorAll('#shelf .chip')].find((c) => c.dataset.title === title);
    const to = chip && chip.offsetWidth ? chip.getBoundingClientRect() : $('shelf').getBoundingClientRect();
    if (chip) chip.style.visibility = 'hidden';
    const move = `translate(${to.left - from.left}px, ${to.top - from.top}px) scale(${to.width / from.width}, ${to.height / from.height})`;
    ghost.animate([{ transform: 'none', opacity: 1 }, { transform: move, opacity: .35 }], { duration: 420, easing: 'cubic-bezier(.5, 0, .75, .4)', fill: 'forwards' }).onfinish = () => {
      ghost.remove();
      if (chip) { chip.style.visibility = ''; chip.classList.add('arrive'); }
    };
  };
}

function press() {
  if (sending || on === null) return;
  const prev = on, i = AGENTS.indexOf(prev);
  const keepFocus = document.activeElement === key;
  key.classList.add('down');
  setTimeout(() => key.classList.remove('down'), 100);
  disarm();
  stopNudging();
  clearInterval(typeTimer);
  const level = AGENTS.length - queue.length + 1;
  if (queue.length === 1) { pressLast(prev, i, level); return; }
  if (still) {
    Field.settle(i, lightOrigin(), level);
    settleOn(); render(); done(keepFocus);
    return;
  }
  sending = true;
  root.classList.add('sending');
  renderTerm($('f-term'), prev, { work: true, sent: true });
  later(280, () => {
    $('front').classList.add('done');
    rowFor.get(prev).classList.add('done');
    Field.settle(i, lightOrigin(), level);
  });
  later(650, () => {
    const fly = flyToShelf($('front'), prev.title);
    settleOn();
    render({ type: false });
    $('front').style.opacity = '0';
    fly();
  });
  later(980, () => {
    $('front').animate([{ transform: 'translateX(56px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 320, easing: 'cubic-bezier(.2, .7, .3, 1)' });
    $('front').style.opacity = '';
  });
  later(1320, () => {
    sending = false;
    root.classList.remove('sending');
    typeReply(on);
    done(keepFocus);
  });
}

function done(keepFocus) {
  if (keepFocus) key.focus({ preventScroll: true });
  arm();
  startNudging();
}

const win = $('win'), homepane = $('homepane'), installBox = document.querySelector('.cmds'), homeLinks = document.querySelector('.homelinks'), endStage = $('endstage');
let dissolving = 0;

function pressLast(prev, i, level) {
  sending = true;
  root.classList.add('sending');
  renderTerm($('f-term'), prev, { work: true, sent: true });
  placeStage();
  root.classList.add('ended-stage');
  endStage.append(installBox, homeLinks);
  const breakUp = () => {
    const leave = windowLeave(lightOrigin());
    dissolveWindow(leave);
    Field.gather(leave, installArrives);
  };
  if (still) { Field.settle(i, lightOrigin(), level); breakUp(); return; }
  later(280, () => {
    $('front').classList.add('done');
    rowFor.get(prev).classList.add('done');
    Field.settle(i, lightOrigin(), level);
  });
  later(460, breakUp);
}

function installArrives() {
  settleOn();
  stopAuto();
  sending = false;
  root.classList.remove('sending');
  rowsEl.replaceChildren();
  document.title = 'attn';
  $('again').textContent = 'Run again';
  $('status').textContent = 'Nothing is waiting. The install commands are ready to copy.';
  root.classList.add('arrived');
  copy.focus({ preventScroll: true });
}

function clearEnding() {
  cancelAnimationFrame(dissolving);
  win.classList.remove('gone');
  win.style.maskImage = win.style.webkitMaskImage = win.style.imageRendering = '';
  root.classList.remove('ended-stage', 'arrived');
  homepane.append(installBox, homeLinks);
}

function placeStage() {
  const hero = $('hero').getBoundingClientRect(), box = win.getBoundingClientRect();
  const bottom = phone.matches ? hero.bottom - 14 : box.bottom;
  Object.assign(endStage.style, { left: box.left - hero.left + 'px', top: box.top - hero.top + 'px', width: box.width + 'px', height: bottom - box.top + 'px' });
}
addEventListener('resize', () => { if (root.classList.contains('ended-stage')) placeStage(); }, { passive: true });

function windowLeave(origin) {
  const hero = $('hero').getBoundingClientRect(), box = win.getBoundingClientRect();
  const cell = 12, across = Math.ceil(box.width / cell), down = Math.ceil(box.height / cell);
  const reach = Math.hypot(Math.max(origin.x - box.left, box.right - origin.x), Math.max(origin.y - box.top, box.bottom - origin.y));
  const at = Float32Array.from({ length: across * down }, (_, i) => {
    const away = Math.hypot(box.left + (i % across + .5) * cell - origin.x, box.top + (Math.floor(i / across) + .5) * cell - origin.y);
    return .65 * away / reach + .35 * (hash(i * 13) % 1000) / 1000;
  });
  return { cell, across, down, at, left: box.left - hero.left, top: box.top - hero.top };
}

function dissolveWindow(leave) {
  if (still) { win.classList.add('gone'); return; }
  const mask = document.createElement('canvas');
  mask.width = leave.across; mask.height = leave.down;
  const g = mask.getContext('2d');
  const start = performance.now();
  win.style.maskSize = win.style.webkitMaskSize = '100% 100%';
  win.style.imageRendering = 'pixelated';
  const step = (now) => {
    const done = (now - start) / DISSOLVE_MS;
    if (done >= 1) { win.classList.add('gone'); win.style.maskImage = win.style.webkitMaskImage = ''; return; }
    g.clearRect(0, 0, mask.width, mask.height);
    for (let i = 0; i < leave.at.length; i++) if (leave.at[i] > done) g.fillRect(i % leave.across, Math.floor(i / leave.across), 1, 1);
    win.style.maskImage = win.style.webkitMaskImage = `url(${mask.toDataURL()})`;
    dissolving = requestAnimationFrame(step);
  };
  dissolving = requestAnimationFrame(step);
}

function jumpTo(a) {
  if (!queue.includes(a) || sending) return;
  stopAuto();
  on = a; view = 'agent';
  render();
  key.focus({ preventScroll: true });
  startNudging();
}

function goHome() {
  if (sending) return;
  if (on === null) { copy.focus({ preventScroll: true }); return; }
  stopAuto();
  stopNudging();
  view = 'home';
  render();
  copy.focus({ preventScroll: true });
}

function restart() {
  const button = $('again').getBoundingClientRect();
  clearEnding();
  reset(); auto = false;
  Field.restart({ x: button.left + button.width / 2, y: button.top + button.height / 2 });
  render();
  key.focus({ preventScroll: true });
  startNudging();
}

const SHAKE = [[0, 1, 0, 0], [-8, 1.04, -1.5, .6], [8, 1.06, 1.5, 1], [-7, 1.06, -1.2, 1], [6, 1.05, 1, .8], [-4, 1.03, -.6, .5], [2, 1.01, .3, .2], [0, 1, 0, 0]]
  .map(([x, grow, turn, glow]) => ({ transform: `translateX(${x}px) rotate(${turn}deg) scale(${grow})`, filter: `brightness(${1 + glow * .2}) drop-shadow(0 0 ${glow * 12}px rgb(255 122 69 / ${glow * .9}))` }));
let nudgeTimer = 0, shaking = null;
function shake() { if (!document.hidden && paneSeen) shaking = key.animate(SHAKE, { duration: 620 }); }
function startNudging() {
  stopNudging();
  if (still) return;
  shake();
  nudgeTimer = setInterval(shake, NUDGE_MS);
}
function stopNudging() { clearInterval(nudgeTimer); shaking?.cancel(); }

function arm() {
  disarm();
  if (!auto || on === null || sending) return;
  keywrap.style.setProperty('--dwell', DWELL_MS + 'ms');
  void keywrap.offsetWidth;
  keywrap.classList.add('counting');
}
function disarm() { keywrap.classList.remove('counting'); clearTimeout(stillTimer); stillTimer = 0; }
function stopAuto() { auto = false; disarm(); for (const t of ACTIVITY) removeEventListener(t, onActivity); }
function onActivity() {
  if (!auto) return;
  keywrap.classList.remove('counting');
  clearTimeout(stillTimer);
  stillTimer = setTimeout(arm, STILL_MS);
}
const ACTIVITY = ['pointermove', 'pointerdown', 'wheel', 'touchstart', 'scroll'];
function applyHeld() { root.classList.toggle('held', document.hidden || !paneSeen); }
function humanSettle() { stopAuto(); press(); }

$('fill').addEventListener('animationend', () => { if (auto && on !== null) press(); });
key.addEventListener('click', humanSettle);
$('homebtn').addEventListener('click', goHome);
$('jump').addEventListener('click', (e) => { e.preventDefault(); $('win').scrollIntoView({ block: 'nearest' }); goHome(); });

async function copyInstall() {
  try { await navigator.clipboard.writeText(INSTALL); }
  catch {
    const area = document.createElement('textarea');
    area.value = INSTALL; document.body.append(area); area.select();
    document.execCommand('copy'); area.remove();
    copy.focus({ preventScroll: true });
  }
  $('copylabel').textContent = 'Copied';
  $('status').textContent = 'Install commands copied.';
  setTimeout(() => { $('copylabel').textContent = 'Copy'; }, 1800);
}
copy.addEventListener('click', copyInstall);

$('again').addEventListener('click', () => {
  if (on === null) restart();
  else { view = 'agent'; render(); key.focus({ preventScroll: true }); startNudging(); }
});

document.addEventListener('keydown', (e) => {
  if (e.key !== 'Enter' || e.repeat || e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
  const active = document.activeElement;
  if (active && active !== document.body) return;
  const homeShown = on === null || view === 'home';
  if (homeShown) { if (paneSeen) { e.preventDefault(); copy.focus({ preventScroll: true }); copyInstall(); } return; }
  if (paneSeen) { e.preventDefault(); humanSettle(); }
});
document.addEventListener('visibilitychange', applyHeld);
new IntersectionObserver(([e]) => { paneSeen = e.intersectionRatio >= .5; applyHeld(); root.classList.toggle('past', e.intersectionRatio < .15); }, { threshold: [0, .15, .5, 1] }).observe($('win'));

reset();
render();
document.fonts.load('800 100px Mona').then(() => document.fonts.load('12px JBM')).finally(() => {
  Field.start();
  if (auto) { for (const t of ACTIVITY) addEventListener(t, onActivity, { passive: true }); arm(); }
  startNudging();
});
