import { noise, OCEAN_HEIGHT, OCEAN_WIDTH, SEA_LEVEL } from './water';
import type { Ocean } from './ocean';
import { drawSurfer } from './paintSurfer';
import { drawSurferModel } from './paintSurferModel';
import { drawLip, drawTube, drawWaveFace, tubeOpening, type WaterRect } from './paintWave';
import { drawBarrelBack, drawBarrelFront } from './paintBarrel';
import type { BarrelFrame } from './barrelGeometry';
import { drawBarrelSetting } from './paintBarrelSetting';

type Rect = WaterRect;
const waterColors = ['#83e9e4', '#4dd3db', '#34bace', '#28a5c1', '#258fb5', '#247da9', '#246da0'];

// Explicit art poses allow frozen studies without advancing or changing the live simulation.
export type OceanArtwork = { barrels: readonly BarrelFrame[]; surferScale?: number; detailedSurfer?: boolean };

export function drawOcean(ctx: CanvasRenderingContext2D, ocean: Ocean, reducedMotion: boolean, artwork?: OceanArtwork) {
  ctx.imageSmoothingEnabled = false;
  const rect: Rect = (x, y, w, h, color) => {
    if (w <= 0 || h <= 0) return;
    ctx.fillStyle = color;
    ctx.fillRect(Math.round(x), Math.round(y), Math.ceil(w), Math.ceil(h));
  };
  const t = reducedMotion ? 0 : ocean.time;
  const { camera } = ocean;
  if (artwork) drawBarrelSetting(rect, ocean);
  else drawOceanSetting(ctx, rect, ocean, t);
  const visibleWaves = artwork ? [] : ocean.waves.filter(wave =>
    wave.x + wave.shape.crestLength + wave.shape.backWidth * 2 > camera.x - 250
    && wave.x - wave.shape.frontWidth * 3 < camera.x + OCEAN_WIDTH + 250);
  for (const wave of visibleWaves) {
    drawWaveFace(ctx, rect, wave, t, camera);
    drawTube(ctx, rect, wave, t, camera);
  }
  for (const barrel of artwork?.barrels ?? []) drawBarrelBack(ctx, barrel, camera);
  drawFish(rect, t, ocean);
  drawGround(ctx, rect, ocean);

  const barrel = ocean.barrel;
  const cover = ocean.cover;
  let paintedSurfer = false;
  for (let z = 100; z >= 0; z -= 5) {
    if (!paintedSurfer && ocean.z >= z) {
      if (artwork?.detailedSurfer) drawSurferModel(ctx, ocean, t, artwork.surferScale);
      else {
        if (artwork?.surferScale) {
          const feet = ocean.project(ocean.x, ocean.y, ocean.z);
          ctx.save(); ctx.translate(feet.x, feet.y); ctx.scale(artwork.surferScale, artwork.surferScale);
          ctx.translate(-feet.x, -feet.y);
        }
        drawSurfer(ctx, ocean, t);
        if (artwork?.surferScale) ctx.restore();
      }
      if (ocean.state === 'wading') {
        const feet = ocean.project(ocean.x, ocean.y, ocean.z);
        const water = ocean.project(ocean.x, ocean.surface(ocean.x, ocean.z), ocean.z);
        ctx.save(); ctx.globalAlpha = 0.45;
        rect(feet.x - 7, water.y, 14, feet.y - water.y + 1, '#55d3d9');
        ctx.restore();
      }
      paintedSurfer = true;
    }
    for (const wave of visibleWaves) {
      if (z === 0 || z === 100) {
        ctx.save();
        ctx.globalAlpha = (z === 0 ? 1 : 0.45) * (z < ocean.z && barrel === wave ? 1 - cover * 0.72 : 1);
        drawLip(ctx, rect, wave, t, z, camera);
        ctx.restore();
      }
    }
    for (const p of ocean.particles) {
      if (p.z < z || p.z >= z + 5) continue;
      const position = ocean.project(p.x, p.y, p.z);
      if (p.kind === 'bubble') rect(position.x, position.y, p.size + 1, 1, '#8ce2dc');
      else rect(position.x, position.y, p.kind === 'foam' ? p.size + 2 : p.size, p.size, p.life > 0.5 ? '#e3fff0' : '#8ce3df');
    }
  }
  for (const barrel of artwork?.barrels ?? []) drawBarrelFront(ctx, barrel, camera);
  if (cover > 0 && barrel) {
    ctx.save(); ctx.globalAlpha = cover * 0.07;
    tubeOpening(ctx, barrel, 0, camera); ctx.fillStyle = '#6ed9df'; ctx.fill();
    ctx.restore();
  }
}

function drawOceanSetting(ctx: CanvasRenderingContext2D, rect: Rect, ocean: Ocean, t: number) {
  drawSky(ctx, rect, t, ocean);
  const { camera } = ocean;
  const horizon = ocean.project(0, SEA_LEVEL, 100).y;
  rect(0, horizon, OCEAN_WIDTH, OCEAN_HEIGHT - horizon, '#38b9cb');
  for (let screenX = 0; screenX < OCEAN_WIDTH; screenX += 2) {
    const x = screenX + camera.x;
    const top = ocean.surface(x) - camera.y;
    const floor = ocean.floor(x) - camera.y;
    for (let band = 0; band < waterColors.length; band++) {
      const y = top + band * (7 + band * 0.6);
      if (y < floor) rect(screenX, y, 2, floor - y + 1, waterColors[band]);
    }
    if (floor > top) rect(screenX, top, 2, 1, '#b4f7e8');
  }

  const firstTile = Math.floor(camera.x / OCEAN_WIDTH);
  for (let tile = firstTile; tile <= firstTile + 1; tile++) {
    for (let i = 0; i < 280; i++) {
      const seed = tile * 281 + i;
      const x = tile * OCEAN_WIDTH + noise(seed + 30) * OCEAN_WIDTH;
      const top = ocean.surface(x); const floor = ocean.floor(x);
      if (floor <= top + 10) continue;
      const depth = noise(seed + 219);
      const y = top + 5 + depth * Math.min(180, floor - top - 9);
      rect(x - camera.x, y - camera.y, 2 + noise(seed + 17) * 5, 1 + noise(seed + 13) * 3,
        depth < 0.2 ? '#69d9dc' : depth < 0.5 ? '#35aec4' : '#2985b0');
    }
  }

  drawWaterSurface(ctx, rect, ocean, t);
}

function drawSky(ctx: CanvasRenderingContext2D, rect: Rect, t: number, ocean: Ocean) {
  const sky = ocean.beach.id === 'nazare' ? ['#91adbf', '#9bb5c4', '#a5beca', '#b4c9d0', '#c0d1d5', '#ccdad9'] : ['#91bff0', '#95c3f2', '#99c8f4', '#9dccf4', '#a3d1f4', '#b0daf3'];
  const skyHeight = Math.max(SEA_LEVEL, SEA_LEVEL - ocean.camera.y);
  for (let i = 0; i < sky.length; i++) rect(0, i * skyHeight / sky.length, OCEAN_WIDTH, skyHeight / sky.length + 1, sky[i]);
  for (let y = -13; y <= 13; y++) {
    const width = Math.sqrt(169 - y * y);
    rect(112 - width, 75 + y, width * 2, 1, '#f2f3cf');
  }
  for (let i = 0; i < 4; i++) {
    const span = OCEAN_WIDTH + 180;
    const x = ((i * 241 + 260 - t * 0.5) % span + span) % span - 90;
    const y = SEA_LEVEL - 101 + noise(i + 49) * 41;
    const width = 25 + noise(i + 11) * 25;
    rect(x, y, width + 15, 3, '#cbe8f5');
    rect(x + 4, y - 3, width + 7, 4, '#edf9f5');
    rect(x + 12, y - 6, width - 7, 4, '#edf9f5');
    rect(x + 19, y - 8, width - 23, 3, '#edf9f5');
  }
  ctx.save(); ctx.translate(-ocean.camera.x * 0.06, -ocean.camera.y * 0.78);
  ctx.fillStyle = '#789bb5';
  ctx.beginPath(); ctx.moveTo(0, SEA_LEVEL - 12);
  for (let x = 0; x <= 290; x += 5) ctx.lineTo(x, Math.round(SEA_LEVEL - 18 - Math.sin(x * 0.022) ** 2 * 11));
  ctx.lineTo(290, SEA_LEVEL - 11); ctx.fill();
  ctx.fillStyle = '#3c657c';
  ctx.beginPath(); ctx.moveTo(0, SEA_LEVEL - 11);
  for (let x = 0; x <= 212; x += 4) {
    const height = Math.max(0, 33 * Math.sin((x + 30) * 0.013) + Math.sin(x * 0.053) * 6);
    ctx.lineTo(x, Math.round(SEA_LEVEL - 13 - height));
  }
  ctx.lineTo(212, SEA_LEVEL - 11); ctx.fill();
  for (let i = 0; i < 52; i++) {
    const x = noise(i + 5) * 195;
    const ridge = SEA_LEVEL - 13 - Math.max(0, 33 * Math.sin((x + 30) * 0.013) + Math.sin(x * 0.053) * 6);
    rect(x, ridge + noise(i + 55) * 11, 3 + noise(i) * 6, 2 + noise(i + 7) * 3, i % 3 ? '#537d75' : '#789878');
  }
  rect(0, SEA_LEVEL - 13, 218, 2, '#baded6');
  if (ocean.beach.id === 'nazare') {
    rect(66, SEA_LEVEL - 68, 13, 21, '#e7d9b9');
    rect(64, SEA_LEVEL - 72, 17, 4, '#aa6555');
    rect(69, SEA_LEVEL - 77, 7, 5, '#344c5f');
  }
  ctx.restore();
  for (let i = 0; i < 3; i++) {
    const x = 335 + i * 11 + Math.sin(t * 0.025) * 35;
    const y = SEA_LEVEL - 92 + i % 2 * 5;
    const wing = Math.sin(t * 2 + i) > 0 ? -1 : 1;
    rect(x - 3, y + wing, 3, 1, '#6999bf');
    rect(x, y, 2, 1, '#6999bf');
    rect(x + 2, y + wing, 3, 1, '#6999bf');
  }
}


function drawWaterSurface(ctx: CanvasRenderingContext2D, rect: Rect, ocean: Ocean, t: number) {
  const colors = ['#279daf', '#2caabc', '#34b9c9', '#40c6d1', '#55d3d9'];
  const left = Math.floor((ocean.camera.x - 260) / 4) * 4;
  const right = left + OCEAN_WIDTH + 520;
  for (let z = 100; z > 0; z -= 10) {
    ctx.beginPath();
    let first = true;
    const vertex = (x: number, depth: number) => {
      const surface = ocean.surface(x, depth);
      if (ocean.floor(x, depth) <= surface) return;
      const p = ocean.project(x, surface, depth);
      if (first) { ctx.moveTo(Math.round(p.x), Math.round(p.y)); first = false; }
      else ctx.lineTo(Math.round(p.x), Math.round(p.y));
    };
    for (let x = left; x <= right; x += 4) vertex(x, z);
    for (let x = right; x >= left; x -= 4) vertex(x, z - 10);
    ctx.closePath();
    ctx.fillStyle = colors[Math.min(4, Math.floor((100 - z) / 20))]; ctx.fill();
    for (let x = Math.floor(left / 43) * 43; x < right; x += 43) {
      const px = x + Math.sin(t * 0.4 + x) * 3;
      if (ocean.floor(px, z) <= ocean.surface(px, z)) continue;
      const p = ocean.project(px, ocean.surface(px, z), z);
      rect(p.x, p.y, 3 + noise(x + z) * 8, 1, z > 50 ? '#61c5d0' : '#a3ece3');
    }
  }
}

function drawGround(ctx: CanvasRenderingContext2D, rect: Rect, ocean: Ocean) {
  const colors = [ocean.beach.sand, '#c29c68', '#977348', '#715b47', '#4d4b47', '#273e50'];
  for (let x = 0; x < OCEAN_WIDTH; x += 4) {
    const floor = ocean.floor(x + ocean.camera.x) - ocean.camera.y;
    for (let band = 0; band < colors.length; band++) rect(x, floor + band * 6, 4, OCEAN_HEIGHT - floor - band * 6, colors[band]);
  }
  const left = Math.floor((ocean.camera.x - 260) / 8) * 8;
  for (let z = 100; z > 0; z -= 10) {
    for (let x = left; x < left + OCEAN_WIDTH + 520; x += 8) {
      if (ocean.floor(x, z) > ocean.surface(x, z)) continue;
      ctx.beginPath();
      [[x, z], [x + 8, z], [x + 8, z - 10], [x, z - 10]].forEach(([px, depth], index) => {
        const p = ocean.project(px, ocean.floor(px, depth), depth);
        if (!index) ctx.moveTo(Math.round(p.x), Math.round(p.y)); else ctx.lineTo(Math.round(p.x), Math.round(p.y));
      });
      ctx.closePath(); ctx.fillStyle = z > 50 ? '#d7c198' : ocean.beach.sand; ctx.fill();
    }
  }
  for (let x = Math.floor(left / 37) * 37; x < left + OCEAN_WIDTH + 520; x += 37) {
    const y = ocean.floor(x);
    if (y < SEA_LEVEL + 15) {
      const p = ocean.project(x, y, 20);
      rect(p.x, p.y - 1, 2 + noise(x) * 3, 1, '#ac976d');
    } else {
      const p = ocean.project(x, y);
      rect(p.x, p.y - 2, 4 + noise(x) * 5, 2, '#398481');
      rect(p.x + 2, p.y - 5 - noise(x + 45) * 6, 2, 8, '#3e8e87');
    }
  }
}

function drawFish(rect: Rect, t: number, ocean: Ocean) {
  const start = Math.floor(ocean.camera.x / 120) * 120;
  for (let x = start; x < start + OCEAN_WIDTH + 120; x += 120) {
    const px = x + Math.sin(t * 0.15 + x) * 30;
    const y = SEA_LEVEL + 27 + noise(x + 90) * 23 + Math.sin(t * 0.8 + x) * 2;
    if (ocean.floor(px) < y + 8 || ocean.surface(px) > y - 5) continue;
    const p = ocean.project(px, y);
    const size = noise(x) > 0.75 ? 2 : 1;
    rect(p.x, p.y, 5 * size, 3 * size, '#458c9a');
    rect(p.x + 2 * size, p.y, size, 3 * size, '#2c728c');
    rect(p.x - 2 * size, p.y + size, 2 * size, size, '#377c91');
  }
}
