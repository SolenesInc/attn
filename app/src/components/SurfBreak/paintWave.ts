import { clamp, curlPoint, noise, project, SEA_LEVEL, waveLift, waveSection, type Camera, type Wave } from './water';

export type WaterRect = (x: number, y: number, w: number, h: number, color: string) => void;
const snap = (n: number) => Math.round(n / 2) * 2;

export function tubeOpening(ctx: CanvasRenderingContext2D, wave: Wave, z: number, camera?: Camera) {
  const bounds = { left: Infinity, right: -Infinity, top: Infinity, bottom: -Infinity };
  let first = true;
  const vertex = (x: number, y: number) => {
    const p = project(x, y, z, camera);
    bounds.left = Math.min(bounds.left, p.x); bounds.right = Math.max(bounds.right, p.x);
    bounds.top = Math.min(bounds.top, p.y); bounds.bottom = Math.max(bounds.bottom, p.y);
    if (first) ctx.moveTo(snap(p.x), snap(p.y));
    else ctx.lineTo(snap(p.x), snap(p.y));
    first = false;
  };
  ctx.beginPath();
  for (let i = 0; i <= 40; i++) {
    const arc = curlPoint(wave, i / 40, -5, z);
    vertex(arc.x, arc.y);
  }
  const lip = curlPoint(wave, 1, -5, z);
  const { center, height } = waveSection(wave, z);
  for (let x = lip.x; x < center; x += 2) {
    vertex(x, SEA_LEVEL - waveLift(wave, x, z));
  }
  vertex(center, SEA_LEVEL - height);
  ctx.closePath();
  return bounds;
}

export function drawWaveFace(ctx: CanvasRenderingContext2D, screenRect: WaterRect, wave: Wave, t: number, camera: Camera = { x: 0, y: 0 }) {
  const rect: WaterRect = (x, y, w, h, color) => screenRect(snap(x - camera.x), snap(y - camera.y), w, h, color);
  if (wave.height < 3) return;
  const { center, frontWidth, backWidth, crestLength, curl } = waveSection(wave);
  const start = Math.max(center - frontWidth * 2.5, camera.x - 20);
  const end = Math.min(center + crestLength + backWidth * 1.65, camera.x + 820);
  for (let column = Math.floor((start - center) / 4); column * 4 + center <= end; column++) {
    const x = center + column * 4;
    const lift = waveLift(wave, x);
    if (lift < 6) continue;
    const top = SEA_LEVEL - lift;
    const seed = column + wave.id * 97;
    const cap = lift * (0.2 + Math.sin(column * 0.22) * 0.045 + noise(Math.floor(seed / 3)) * 0.07);
    const shade = lift * (0.52 + Math.sin(column * 0.17 + 4) * 0.08 + noise(Math.floor(seed / 4)) * 0.08);
    rect(x, top, 4, lift + 1, '#59c8c5');
    rect(x, top + cap, 4, lift - cap + 1, '#3198ac');
    rect(x, top + shade, 4, lift - shade + 1, '#286d9c');
    for (let patch = 0; patch < 3; patch++) {
      const depth = noise(seed * 11 + patch * 31);
      rect(x, top + depth * lift, 4 + noise(seed + patch) * 5, 3 + noise(seed * 7 + patch) * 9,
        depth < 0.25 ? '#68cecb' : depth < 0.6 ? '#349dae' : '#2c79a0');
    }
  }
  const lip = curl > 0.25 && wave.shape.maxCurl >= 0.45 ? curlPoint(wave, 1) : null;
  for (let column = Math.floor((start - center) / 15); column * 15 + center < end; column++) {
    const origin = center + column * 15 + noise(column) * 5;
    const height = waveLift(wave, origin);
    if (height < 8) continue;
    // Once the lip is pitching, its own curve carries the visible flow. Drawing a
    // second family through that span makes an unrelated break travel backwards.
    if (lip && origin > lip.x - frontWidth * 0.12 && origin < center + crestLength + backWidth * 0.2) continue;
    const steps = Math.min(60, Math.ceil(height / 3));
    for (let j = 0; j < steps; j++) {
      if (noise(column * 23 + Math.floor(j / 3)) < 0.22) continue;
      const flow = (j / steps + t * 0.075 + noise(column + wave.id) * 0.4) % 1;
      const x = origin - (flow * 0.15 + flow * flow * 0.65) * Math.min(height, frontWidth);
      const y = SEA_LEVEL - height + flow * (height + 3);
      if (y < SEA_LEVEL - waveLift(wave, x) || y > SEA_LEVEL + 5) continue;
      const color = flow < 0.18 || flow > 0.86 || column % 3 === 0 ? '#d3f9e9' : '#88ded4';
      rect(x, y, 2 + noise(column + j * 7) * 3, 2 + noise(j + 31) * 2, color);
    }
  }
  ctx.save();
  ctx.globalAlpha = clamp(wave.height / 25, 0, 1);
  for (let i = 0; i < 32; i++) {
    const x = start + noise(i + wave.id * 29) * (end - start);
    if (waveLift(wave, x) < 10) continue;
    rect(x, SEA_LEVEL + 1 + noise(i + 18) * 4, 3 + noise(i) * 8, 1, '#8be2db');
  }
  ctx.restore();
}

export function drawTube(ctx: CanvasRenderingContext2D, rect: WaterRect, wave: Wave, t: number, camera?: Camera) {
  if (wave.shape.maxCurl < 0.45 || wave.curl <= 0.2 || wave.height < 3) return;
  ctx.save();
  ctx.globalAlpha *= clamp((wave.curl - 0.2) / 0.35, 0, 1) * (1 - waveSection(wave).collapse);
  const bounds = tubeOpening(ctx, wave, 0, camera);
  ctx.clip();
  rect(bounds.left - 2, bounds.top - 2, bounds.right - bounds.left + 4, bounds.bottom - bounds.top + 4, '#255c89');
  for (let i = 0; i < 180; i++) {
    const depth = noise(i + 211);
    const x = bounds.left + noise(i + wave.id * 19) * (bounds.right - bounds.left);
    const y = bounds.top + depth * (bounds.bottom - bounds.top);
    rect(snap(x), snap(y), 4 + noise(i + 75) * 8, 4 + noise(i + 17) * 12, depth < 0.55 ? '#214f7c' : '#2d6d95');
  }
  for (let i = 0; i <= 72; i++) {
    const progress = i / 72;
    const arc = curlPoint(wave, progress, -7 - noise(i + wave.id) * wave.height * 0.08, 0);
    const p = project(arc.x, arc.y, 0, camera);
    rect(snap(p.x), snap(p.y), 4 + noise(i + 12) * 7, 3 + noise(i + 43) * 6, '#183e64');
  }
  const { center, frontWidth } = waveSection(wave);
  for (let i = 0; i < 60; i++) {
    const x = center - frontWidth * (0.3 + noise(i + wave.id) * 2);
    const p = project(x, SEA_LEVEL - waveLift(wave, x) - 2 + Math.sin(t * 0.7 + i) * 2, 0, camera);
    rect(snap(p.x), snap(p.y), 5 + noise(i) * 12, 2 + noise(i + 14) * 3, '#60c9c6');
  }
  ctx.restore();
}

function drawCurlBody(ctx: CanvasRenderingContext2D, rect: WaterRect, wave: Wave, t: number, z: number, camera: Camera) {
  const section = waveSection(wave, z);
  const thickness = Math.max(10, section.height * 0.24) * section.curl;
  const point = (arc: number, rim: number) => {
    const p = curlPoint(wave, arc, rim, z);
    return project(p.x, p.y, z, camera);
  };
  ctx.save(); ctx.beginPath();
  for (let i = 0; i <= 64; i++) {
    const p = point(i / 64, thickness);
    if (!i) ctx.moveTo(snap(p.x), snap(p.y)); else ctx.lineTo(snap(p.x), snap(p.y));
  }
  for (let i = 64; i >= 0; i--) {
    const p = point(i / 64, -5);
    ctx.lineTo(snap(p.x), snap(p.y));
  }
  ctx.closePath(); ctx.fillStyle = '#3198ac'; ctx.fill(); ctx.clip();
  for (let stripe = 0; stripe < 12; stripe++) {
    const across = stripe / 11;
    for (let i = 0; i < 65; i++) {
      const flow = (i / 65 + t * 0.065) % 1;
      const p = point(flow, -5 + (thickness + 5) * across);
      const n = noise(Math.floor(i / 3) + stripe * 19 + wave.id);
      const lit = across > 0.72;
      const color = lit ? n > 0.45 ? '#b4eddb' : '#62cfcc' : n > 0.7 ? '#79d6cf' : '#3aadb5';
      rect(snap(p.x), snap(p.y), 3 + n * 5, 3 + noise(i + stripe) * 6, color);
    }
  }
  ctx.restore();
  for (let i = 0; i < 75; i++) {
    if (noise(Math.floor(i / 3) + wave.id) < 0.18) continue;
    const p = point((i / 75 + t * 0.065) % 1, thickness);
    rect(snap(p.x), snap(p.y), 3 + noise(i) * 5, 2 + noise(i + 20) * 3, '#e3fff0');
  }
}

export function drawLip(ctx: CanvasRenderingContext2D, rect: WaterRect, wave: Wave, t: number, z: number, camera: Camera = { x: 0, y: 0 }) {
  if (wave.height < 2) return;
  const { center, frontWidth, backWidth, crestLength, breaking } = waveSection(wave, z);
  const start = Math.max(center - frontWidth * 2.5, camera.x - 220);
  const end = Math.min(center + crestLength + backWidth * 1.65, camera.x + 1020);
  for (let x = start; x < end; x += 3) {
    if (waveLift(wave, x, z) < 4) continue;
    const p = project(x, SEA_LEVEL - waveLift(wave, x, z), z, camera);
    const n = noise(Math.floor((x - center) / 3) + wave.id);
    if (n > 0.16) rect(snap(p.x), snap(p.y) - n * 2, 4 + n * 4, 2 + n * 2 + breaking * 4, '#dbffef');
  }
  if (breaking > 0) {
    for (let i = 0; i < 90; i++) {
      const x = center - frontWidth * 1.4 + noise(i + wave.id) * (frontWidth * 1.4 + crestLength);
      const p = project(x, SEA_LEVEL - waveLift(wave, x, z), z, camera);
      rect(p.x, p.y + noise(i + 92) * 10 * breaking, 3 + breaking * noise(i + 22) * 12, 1 + breaking * 2, '#b4f3e4');
    }
  }
  if (wave.curl < 0.25 || wave.shape.maxCurl < 0.45) return;
  drawCurlBody(ctx, rect, wave, t, z, camera);
  const lip = curlPoint(wave, 1, 0, z);
  for (let i = 0; i < 45; i++) {
    const spread = noise(i + wave.id * 7);
    const x = lip.x - spread * 45 + noise(i + 22) * 20;
    const y = SEA_LEVEL - waveLift(wave, x, z);
    const p = project(x, y, z, camera);
    rect(snap(p.x), snap(p.y) - noise(i + 31) * 5, 4 + spread * 12, 2 + noise(i + 56) * 5, '#d3f9e9');
  }
}
