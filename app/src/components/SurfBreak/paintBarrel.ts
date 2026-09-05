import { barrelDisplayU, barrelFlowCoordinates, barrelFlowPoint, barrelLip, barrelLipContour, barrelLipImpact, barrelSample, curtainFlowPoint, curtainSample, type BarrelFrame } from './barrelGeometry';
import { clamp, ease, noise, OCEAN_HEIGHT, OCEAN_WIDTH, type Camera } from './water';

const ink = {
  deep: '#0d3959', shadow: '#104564', blue: '#145774', inner: '#196c84',
  face: '#167f92', water: '#168f9e', light: '#30aaa9', glass: '#65cbbb',
  mint: '#a2e8cf', foam: '#d7f7dd', white: '#e9ffe7',
};
const pixel = 2;
const cavity = [ink.deep, '#0e3d5d', '#0f4161', ink.shadow, '#124c6c', ink.blue,
  '#175e7b', '#186580', ink.inner, '#18758b', ink.face, '#168799', ink.water];
const snap = (n: number) => Math.floor(n / pixel) * pixel;
type ColorAt = (u: number, v: number) => string | null;

function raster(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera, colorAt: ColorAt) {
  const left = Math.max(0, snap(frame.left - camera.x));
  const right = Math.min(OCEAN_WIDTH, frame.left + frame.width * 1.15 - camera.x);
  const top = Math.max(0, snap(frame.floor - frame.height * 1.5 - camera.y));
  const bottom = Math.min(OCEAN_HEIGHT, frame.floor + frame.height * 0.14 - camera.y);
  for (let y = top; y < bottom; y += pixel) {
    let previous: string | null = null;
    let start = left;
    const flush = (x: number) => {
      if (previous) { ctx.fillStyle = previous; ctx.fillRect(start, y, x - start, pixel); }
    };
    for (let x = left; x < right; x += pixel) {
      const u = (x + camera.x - frame.left) / frame.width;
      const v = (y + camera.y - frame.floor) / frame.height + 1;
      const color = colorAt(u, v);
      if (color !== previous) { flush(x); previous = color; start = x; }
    }
    flush(Math.ceil(right / pixel) * pixel);
  }
}

function flowingTexture(seed: number, lane: number, travel: number) {
  const column = Math.floor(lane * 118);
  const row = Math.floor(travel * 36 + noise(column + seed) * 1.5);
  const cell = noise(column * 619 + row * 37 + seed * 71);
  const fold = Math.sin(lane * 53 + Math.sin(lane * 19) * 0.6);
  return { cell, fold, patch: cell > 0.62 ? 1 : cell < 0.18 ? -0.65 : 0 };
}

export function barrelColor(frame: BarrelFrame, u: number, v: number): string | null {
  const shape = barrelSample(frame, u, v);
  if (!shape.inside) return null;
  const { lane, travel } = barrelFlowCoordinates(frame, u, v);
  const texture = flowingTexture(frame.seed, lane, travel);
  const n = texture.cell;
  const flow = texture.fold * 0.023;
  const grain = flow + texture.patch * 0.015;
  const fromRoof = (v - shape.roof) / (1.4 - frame.roundness * 0.3);
  if (fromRoof < 0.025 + grain * 0.3) return n > 0.2 ? ink.foam : ink.mint;
  if (fromRoof < 0.052 + grain) return ink.glass;
  if (fromRoof < 0.105 + grain) return ink.light;
  if (fromRoof < 0.155 + grain) return ink.water;
  if (shape.hollow) {
    const rounded = Math.hypot((shape.localU - 0.32) / 0.72, (shape.depth - 0.31) / 1.04);
    const long = Math.max(0, shape.depth - 0.18) * 0.65 + Math.max(0, shape.localU - 0.4) * 0.5;
    const shade = long + (rounded - long) * frame.roundness
      + flow + (n - 0.5) * 0.055 + (1 - shape.shelter) * 0.48 + Math.max(0, shape.localU - 0.46) * 0.52
      + ease((shape.depth - 0.78) / 0.22) * 0.24;
    const band = Math.floor(clamp((shade - 0.15) * 15 + texture.patch * 1.15, 0, cavity.length - 1));
    if (band === 0 && texture.patch !== 0) return texture.patch > 0 ? '#0e3d5d' : '#0c3656';
    return cavity[band];
  }
  if (v > 1.02) return v < 1.065 + flow ? ink.light : ink.water;
  if (v > shape.bottom && v < shape.bottom + 0.075 + grain) return ink.glass;
  const fold = texture.fold * 0.05 + texture.patch * 0.035;
  if (v < shape.bottom + 0.18 + fold) return texture.patch > 0.5 ? '#46b8b0' : ink.light;
  if (v < shape.bottom + 0.4 + fold) return texture.patch > 0.5 ? '#209da4' : ink.water;
  return texture.patch > 0.5 ? ink.water : ink.face;
}

function dab(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera, u: number, v: number, w: number, h: number, color: string) {
  const x = snap(frame.left + u * frame.width - camera.x);
  const y = snap(frame.floor + (v - 1) * frame.height - camera.y);
  if (x + w < 0 || x > OCEAN_WIDTH || y + h < 0 || y > OCEAN_HEIGHT) return;
  ctx.fillStyle = color;
  ctx.fillRect(x, y, w, h);
}

function drawFoam(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera) {
  if (frame.treatment === 'curtain') {
    drawLipDrivenFoam(ctx, frame, camera);
    return;
  }
  for (let strand = 0; strand < 67; strand++) {
    const lane = 0.055 + strand * 0.022 + noise(strand + frame.seed) * 0.01;
    const major = strand % 4 === 0;
    for (let step = 0; step < 128; step++) {
      const t = step / 127;
      const { u, v } = barrelFlowPoint(frame, lane, t);
      const sample = barrelSample(frame, u, v);
      if (!sample.inside) continue;
      const shadowed = sample.hollow && sample.depth > 0.08 && sample.depth < 0.9 && sample.shelter > 0.85;
      const n = noise(strand * 149 + Math.floor(t * 54) + frame.seed);
      if ((shadowed && lane < 0.62) || n < (major ? 0.10 : 0.56)) continue;
      const bright = t < 0.07 || t > 0.93;
      const color = bright ? ink.foam : major ? n > 0.38 || t > 0.62 ? ink.mint : ink.glass
        : n > 0.9 ? ink.glass : ink.light;
      dab(ctx, frame, camera, u, v, major && n > 0.73 ? 4 : 2, 2, color);
    }
  }
  for (let row = 0; row < 5; row++) {
    for (let cell = 0; cell < 225; cell++) {
      const lane = 0.004 + cell / 195;
      const t = row * 0.018 + noise(cell + frame.seed) * 0.012;
      const { u, v } = barrelFlowPoint(frame, lane, t);
      const n = noise(cell * 31 + row * 113 + frame.seed);
      if (n < 0.22 || !barrelSample(frame, u, v).inside) continue;
      dab(ctx, frame, camera, u, v, n > 0.55 ? 4 : 2, 2, row < 2 ? ink.foam : n > 0.55 ? ink.mint : ink.glass);
    }
  }
  for (let row = 0; row < 5; row++) {
    for (let cell = 0; cell < 200; cell++) {
      const lane = cell / 150;
      const n = noise(cell * 29 + row * 71 + frame.seed);
      const { u, v } = barrelFlowPoint(frame, lane, 0.92 + row * 0.031 + n * 0.012);
      if (n < 0.38 || !barrelSample(frame, u, v).inside) continue;
      dab(ctx, frame, camera, u, v, n > 0.8 ? 4 : 2, 2, row === 2 || n > 0.86 ? ink.foam : ink.mint);
    }
  }
}

function drawLipDrivenFoam(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera) {
  for (let band = 0; band < 9; band++) {
    const inset = 0.08 + band * 0.082;
    const major = band % 3 === 0;
    for (let step = 0; step <= 180; step++) {
      const t = step / 180;
      const { u, v } = barrelLipContour(frame, t, inset);
      const n = noise(band * 193 + Math.floor(t * 73) + frame.seed);
      if (n < (major ? 0.26 : 0.69) || !barrelSample(frame, u, v).inside) continue;
      const color = band < 2 ? ink.mint : major ? ink.glass : n > 0.91 ? ink.light : '#299fa3';
      dab(ctx, frame, camera, u, v, major && n > 0.82 ? 4 : 2, 2, color);
    }
  }

  const impact = barrelLipImpact(frame);
  for (let i = 0; i < 115; i++) {
    const n = noise(i * 37 + frame.seed);
    const spread = noise(i * 71 + frame.seed) ** 1.8;
    const u = impact.u + spread * 0.31;
    const v = impact.v - noise(i * 29 + frame.seed) * (0.025 + (1 - spread) * 0.08);
    if (n < 0.31 || !barrelSample(frame, u, v).inside) continue;
    dab(ctx, frame, camera, u, v, n > 0.78 ? 6 : 2, n > 0.88 ? 4 : 2,
      n > 0.84 ? ink.foam : n > 0.54 ? ink.mint : ink.glass);
  }
}

function drawBreakingLip(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera) {
  const orientation = frame.curlSide === 'right' ? -1 : 1;
  for (let i = 0; i <= 160; i++) {
    const t = i / 160;
    const { u, v, du, dv } = barrelLip(frame, t);
    const dx = du * frame.width;
    const dy = dv * frame.height;
    const length = Math.hypot(dx, dy);
    const thickness = frame.height * (frame.treatment === 'curtain'
      ? (0.07 + frame.lipFall * 0.045) * (0.45 + Math.sin(t * Math.PI) * 0.75)
      : (0.08 + frame.roundness * 0.085) * (1 + Math.sin(t * Math.PI) * 0.35));
    for (let d = thickness; d >= 0; d -= pixel) {
      const n = noise(Math.floor(i / 4) + Math.floor(d / 4) * 67 + frame.seed);
      const color = d < 2 ? ink.foam : d < thickness * 0.35 ? n > 0.38 ? ink.foam : ink.mint
        : d < thickness * 0.6 ? n > 0.62 ? ink.mint : ink.glass
          : d < thickness * 0.82 ? n > 0.72 ? ink.glass : ink.light : ink.water;
      dab(ctx, frame, camera, u + orientation * dy / length * d / frame.width,
        v - orientation * dx / length * d / frame.height, 4, 2, color);
    }
  }
}

export function drawBarrelBack(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera) {
  if (frame.width <= 0 || frame.height <= 0) return;
  ctx.save();
  for (let row = 0; row < 12; row++) {
    const width = 0.78 + Math.sin(row / 12 * Math.PI) * 0.4;
    const edgeA = barrelDisplayU(frame, 0.4 - width / 2);
    const edgeB = barrelDisplayU(frame, 0.4 + width / 2);
    dab(ctx, frame, camera, Math.min(edgeA, edgeB), 1.04 + row * 0.024,
      Math.ceil(frame.width * width / 2) * 2, 2, row % 3 ? '#158a9b' : '#1a92a1');
  }
  raster(ctx, frame, camera, (u, v) => barrelColor(frame, u, v));
  drawFoam(ctx, frame, camera);
  drawBreakingLip(ctx, frame, camera);
  for (let i = 0; i < 120; i++) {
    const lane = noise(i + 311) * 1.3;
    const { u, v } = barrelFlowPoint(frame, lane, 1.02 + noise(i + 83) * 0.08);
    dab(ctx, frame, camera, u, v, 4 + Math.floor(noise(i + 9) * 6) * 2, 2,
      i % 4 === 0 ? ink.glass : ink.light);
  }
  ctx.restore();
}

export function drawBarrelFront(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera) {
  if (frame.width <= 0 || frame.height <= 0 || frame.curtain <= 0) return;
  if (frame.treatment === 'curtain') {
    drawFallingLipFront(ctx, frame, camera);
    return;
  }
  ctx.save();
  ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.38;
  raster(ctx, frame, camera, (u, v) => {
    const sample = curtainSample(frame, u, v);
    if (!sample.inside) return null;
    const texture = flowingTexture(frame.seed, sample.lane, sample.travel);
    const edge = ease(Math.min(sample.across, 1 - sample.across) / 0.13);
    if (texture.cell > edge) return null;
    return texture.patch > 0.5 ? ink.light : texture.fold > 0.2 ? '#259da3' : ink.water;
  });
  ctx.restore();
  ctx.save();
  ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.8;
  for (let strand = 0; strand < 13; strand++) {
    const across = 0.04 + strand / 12 * 0.92;
    const major = strand % 3 === 0;
    for (let step = 0; step < 128; step++) {
      const t = step / 127;
      const { u, v } = curtainFlowPoint(frame, across, t);
      if (!barrelSample(frame, u, v).inside) continue;
      const n = noise(strand * 57 + Math.floor(t * 47) + frame.seed);
      if (n < (major ? 0.12 : 0.57)) continue;
      dab(ctx, frame, camera, u, v, major && n > 0.8 ? 4 : 2, 2,
        t < 0.05 || t > 0.9 ? ink.foam : major ? ink.mint : ink.glass);
    }
  }
  for (let i = 0; i < 70; i++) {
    const { u, v } = curtainFlowPoint(frame, noise(i + frame.seed * 3), 0.98 + noise(i + 78) * 0.06);
    if (!barrelSample(frame, u, v).inside) continue;
    dab(ctx, frame, camera, u, v, 2 + Math.floor(noise(i + 111) * 3) * 2, 2,
      ease(noise(i + 29)) > 0.5 ? ink.foam : ink.mint);
  }
  ctx.restore();
}

function drawFallingLipFront(ctx: CanvasRenderingContext2D, frame: BarrelFrame, camera: Camera) {
  ctx.save();
  ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.34;
  for (let stripe = 0; stripe < 9; stripe++) {
    const across = stripe / 8;
    const major = stripe === 0 || stripe === 4 || stripe === 8;
    for (let step = 0; step <= 110; step++) {
      const travel = step / 110;
      const { u, v } = curtainFlowPoint(frame, across, travel);
      const n = noise(stripe * 131 + Math.floor(travel * 67) + frame.seed);
      if (n < (major ? 0.23 : 0.62)) continue;
      dab(ctx, frame, camera, u, v, major && n > 0.82 ? 4 : 2, 2,
        travel > 0.9 || n > 0.9 ? ink.foam : major ? ink.mint : ink.glass);
    }
  }
  ctx.restore();

  ctx.save();
  ctx.globalAlpha *= clamp(frame.curtain, 0, 1) * 0.72;
  const impact = barrelLipImpact(frame);
  for (let i = 0; i < 48; i++) {
    const n = noise(i * 41 + frame.seed);
    if (n < 0.28) continue;
    dab(ctx, frame, camera, impact.u + noise(i + 21) * 0.2,
      impact.v - noise(i + 43) * 0.055, n > 0.8 ? 6 : 2, 2, n > 0.68 ? ink.foam : ink.mint);
  }
  ctx.restore();
}
