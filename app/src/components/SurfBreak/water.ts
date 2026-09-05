import { beaches, bottomDepth, defaultConditions, swellAmplitude, type Beach } from './beaches';

export const OCEAN_WIDTH = 800;
export const OCEAN_HEIGHT = 450;
export const SEA_LEVEL = 310;
export const GRAVITY = 115;
// Slow the travelling water, not the input, posture, jump or recovery clocks.
export const WAVE_PACE = 0.5;
export const clamp = (n: number, min: number, max: number) => Math.max(min, Math.min(max, n));
export const noise = (n: number) => { const v = Math.sin(n * 127.1 + 311.7) * 43758.5453; return v - Math.floor(v); };
export const ease = (n: number) => { const v = clamp(n, 0, 1); return v * v * (3 - 2 * v); };
export type Camera = { x: number; y: number };
export type WaveShape = {
  kind: 'peeler' | 'roller' | 'hollow' | 'runner';
  frontWidth: number; backWidth: number; crestLength: number;
  barrelWidth: number; barrelHeight: number; maxCurl: number;
};
export type Wave = {
  id: number; x: number; age: number; height: number; amplitude: number;
  curl: number; speed: number; shape: WaveShape; beach: Beach;
  period: number; energy: number; breaking: number; steepness: number; initialGroupSpeed: number; depthGradient: number;
};

// Linear dispersion supplies depth-dependent propagation; breaking below is a game-scale approximation.
export function propagation(depth: number, period: number) {
  const d = Math.max(1, depth);
  const omega = 2 * Math.PI / period;
  let k = Math.max(omega * omega / GRAVITY, omega / Math.sqrt(GRAVITY * d));
  for (let i = 0; i < 5; i++) {
    const th = Math.tanh(k * d);
    k -= (GRAVITY * k * th - omega * omega) / (GRAVITY * (th + k * d * (1 - th * th)));
  }
  const speed = omega / k;
  const kd = Math.min(100, k * d);
  return { speed, groupSpeed: speed * 0.5 * (1 + 2 * kd / Math.sinh(2 * kd)), wavelength: 2 * Math.PI / k };
}

export function createWave(id: number, beach = beaches.sandbar, conditions = defaultConditions(), x = beach.offshore): Wave {
  const variation = noise(id + 17);
  const kind = beach.id === 'cove' ? 'roller' : beach.id === 'point' ? 'runner'
    : beach.id === 'reef' || beach.id === 'nazare' ? 'hollow' : (['peeler', 'roller', 'hollow', 'runner'] as const)[id % 4];
  const period = beach.period * (0.92 + variation * 0.16);
  const motion = propagation(bottomDepth(beach, x), period);
  const width = motion.wavelength;
  const long = kind === 'roller' || kind === 'runner';
  const shape: WaveShape = {
    kind, frontWidth: width * (long ? 0.15 : 0.1), backWidth: width * 0.25,
    crestLength: width * (long ? 0.15 : beach.id === 'nazare' ? 0.12 : 0.035),
    barrelWidth: kind === 'runner' ? 4.2 : beach.id === 'nazare' ? 3.2 : 1.5,
    barrelHeight: long ? 0.85 : 1.08, maxCurl: kind === 'roller' ? 0.18 : beach.hollow,
  };
  const amplitude = swellAmplitude(beach, conditions) * (0.8 + variation * 0.35) * (kind === 'roller' ? (beach.id === 'cove' ? 0.9 : 0.4) : 1);
  const wave: Wave = { id, x, age: 0, height: amplitude, amplitude, curl: 0, speed: motion.speed,
    shape, beach, period, energy: 1, breaking: 0, steepness: 0, initialGroupSpeed: motion.groupSpeed, depthGradient: 0 };
  evolveWave(wave);
  return wave;
}

export function evolveWave(wave: Wave, dt = 0) {
  const depth = Math.max(1, bottomDepth(wave.beach, wave.x, 40));
  wave.depthGradient = (bottomDepth(wave.beach, wave.x, 0) - bottomDepth(wave.beach, wave.x + 100 * wave.beach.peel, 100)) / depth * 0.0025;
  const motion = propagation(depth, wave.period);
  wave.speed = motion.speed * WAVE_PACE;
  const focusing = wave.beach.id === 'nazare' ? 1 + 0.45 * Math.exp(-(((wave.x - 1450) / 480) ** 2)) : 1;
  const shoaled = wave.amplitude * Math.sqrt(clamp(wave.initialGroupSpeed / motion.groupSpeed, 0.6, 3.6)) * focusing;
  const ratio = shoaled / (depth * 0.78);
  wave.steepness = ease((ratio - 0.35) / 0.65);
  if (ratio > 0.95 || wave.breaking > 0) {
    wave.breaking = clamp(wave.breaking + dt * wave.speed / wave.beach.breakRun * clamp(ratio, 0.8, 2), 0, 1);
    wave.energy *= Math.exp(-dt * wave.speed / wave.beach.breakRun * (0.12 + collapseAt(wave.breaking) * 2.8));
  }
  const collapse = collapseAt(wave.breaking);
  const bore = Math.min(shoaled, depth * 0.75);
  wave.height = (shoaled * (1 - collapse) + bore * collapse) * Math.sqrt(wave.energy);
  wave.curl = wave.shape.maxCurl * ease((ratio - 0.5) / 0.55) * (1 - collapse);
}

// The lip can peel while the face still carries energy; closure happens later in the break.
const collapseAt = (breaking: number) => ease((breaking - 0.55) / 0.45);

// Face, overhang, shadow and rider sample the same cross section, including its changing depth line.
export function waveSection(wave: Wave, z = 0) {
  const depthFactor = clamp(1 + wave.depthGradient * (z - 40), 0.7, 1.3);
  const height = wave.height * depthFactor;
  const curl = clamp(wave.curl * depthFactor ** 2, 0, wave.shape.maxCurl);
  const breaking = clamp(wave.breaking + (depthFactor - 1) * wave.breaking, 0, 1);
  const collapse = collapseAt(breaking);
  const steepness = clamp(wave.steepness * depthFactor ** 2, 0, 1) * (1 - collapse) * (0.4 + wave.shape.maxCurl * 0.6);
  const hollowing = curl * (1 - collapse);
  return {
    center: wave.x + z * wave.beach.peel - height * (steepness * 0.22 + hollowing * 0.12),
    frontWidth: wave.shape.frontWidth * (1.3 - steepness * 0.8 - hollowing * 0.1 + collapse * 0.7),
    backWidth: wave.shape.backWidth * (1 + steepness * 0.15 + collapse * 0.45),
    crestLength: wave.shape.crestLength * (1 - steepness * 0.45) + collapse * 65,
    steepness, breaking, collapse, hollowing, height, curl,
  };
}

export function waveLift(wave: Wave, x: number, z = 0) {
  const section = waveSection(wave, z);
  if (x >= section.center) {
    const d = Math.max(0, x - section.center - section.crestLength) / section.backWidth;
    return section.height * Math.exp(-(d ** 2));
  }
  const d = (section.center - x) / section.frontWidth;
  const rounded = Math.exp(-(d ** 2));
  const hollow = Math.exp(-d * 2.8) * (1 + d * 2.8);
  const trough = 0.16 * Math.exp(-(((d - 1.6) / 0.65) ** 2)) * (1 - Math.exp(-d * d * 5)) * (1 - section.breaking);
  return section.height * (rounded * (1 - section.steepness) + hollow * section.steepness - trough);
}

export function waterSurface(waves: readonly Wave[], x: number, z: number, time: number) {
  let lift = 0;
  for (const wave of waves) lift += waveLift(wave, x, z);
  return SEA_LEVEL - lift + Math.sin(x * 0.035 + time * 1.15) * 0.8 + Math.sin(x * 0.017 - time * 0.6) * 0.7;
}

export function sampleWater(waves: readonly Wave[], beach: Beach, x: number, z: number, time: number, y = SEA_LEVEL) {
  const surface = waterSurface(waves, x, z, time);
  const slopeX = (waterSurface(waves, x + 2, z, time) - waterSurface(waves, x - 2, z, time)) / 4;
  const slopeZ = (waterSurface(waves, x, z + 1, time) - waterSurface(waves, x, z - 1, time)) / 2;
  const depth = Math.max(12, bottomDepth(beach, x, z));
  let vx = 0; let surfaceVelocity = 0;
  for (const wave of waves) {
    const lift = waveLift(wave, x, z);
    const weight = clamp(lift / depth, -0.3, 0.9);
    vx -= wave.speed * weight * (0.75 + wave.breaking * 0.5);
    surfaceVelocity += wave.speed * (waveLift(wave, x - 2, z) - waveLift(wave, x + 2, z)) / 4;
  }
  const attenuation = Math.exp(-Math.max(0, y - surface) / Math.max(20, depth * 0.45));
  vx *= attenuation;
  const vz = -slopeZ * 4 * attenuation;
  return { surface, slopeX, slopeZ, vx, vz, vy: (surfaceVelocity + slopeX * vx + slopeZ * vz) * attenuation };
}

export function curlPoint(wave: Wave, progress: number, thickness = 0, z = 0) {
  const { center, collapse, hollowing, height, curl } = waveSection(wave, z);
  const p = clamp(progress, 0, 1);
  const reach = wave.shape.barrelWidth * Math.max(32, height * 0.7) * curl * (1 + hollowing * 0.25 - collapse * 0.4);
  const crest = SEA_LEVEL - height;
  const relativeCurl = curl / Math.max(0.001, wave.shape.maxCurl);
  const fall = clamp(ease((relativeCurl - 0.18) / 0.65) + collapse * 0.35, 0, 1);
  const rise = height * 0.08 * curl;
  const surfaceDrop = clamp(SEA_LEVEL - waveLift(wave, center - reach, z) - crest, height * 0.42, height * 1.08);
  const drop = surfaceDrop * (0.18 + fall * 0.82) * wave.shape.barrelHeight;
  const split = 0.72;
  const curve = p <= split
    ? cubicPoint(p / split,
      { x: center, y: crest },
      { x: center - reach * (0.20 + fall * 0.04), y: crest - rise * 0.75 },
      { x: center - reach * 0.70, y: crest - rise },
      { x: center - reach * 0.90, y: crest + drop * 0.035 })
    : cubicPoint((p - split) / (1 - split),
      { x: center - reach * 0.90, y: crest + drop * 0.035 },
      { x: center - reach * 0.95, y: crest + drop * 0.08 },
      { x: center - reach * 0.985, y: crest + drop * 0.55 },
      { x: center - reach, y: crest + drop });
  const { x, y, dx, dy } = curve;
  const length = Math.hypot(dx, dy) || 1;
  const rim = thickness * Math.sin(p * Math.PI / 2) * curl;
  return {
    x: x - dy / length * rim,
    y: y + dx / length * rim,
  };
}

function cubicPoint(t: number, a: { x: number; y: number }, b: { x: number; y: number },
  c: { x: number; y: number }, d: { x: number; y: number }) {
  const q = 1 - t;
  return {
    x: q ** 3 * a.x + 3 * q * q * t * b.x + 3 * q * t * t * c.x + t ** 3 * d.x,
    y: q ** 3 * a.y + 3 * q * q * t * b.y + 3 * q * t * t * c.y + t ** 3 * d.y,
    dx: 3 * q * q * (b.x - a.x) + 6 * q * t * (c.x - b.x) + 3 * t * t * (d.x - c.x),
    dy: 3 * q * q * (b.y - a.y) + 6 * q * t * (c.y - b.y) + 3 * t * t * (d.y - c.y),
  };
}

export function barrelRoof(wave: Wave, x: number, z = 0) {
  const section = waveSection(wave, z);
  if (section.curl < 0.3 || x > section.center) return Infinity;
  const mouth = curlPoint(wave, 1, -5, z);
  // The falling lip turns inward, so its outer edge extends beyond the tip.
  if (x < section.center - (section.center - mouth.x) * 1.1 - 10) return Infinity;
  let previous = curlPoint(wave, 0, -5, z);
  let roof = Infinity;
  for (let i = 1; i <= 32; i++) {
    const point = curlPoint(wave, i / 32, -5, z);
    if (x >= Math.min(previous.x, point.x) && x <= Math.max(previous.x, point.x)) {
      const fraction = (x - previous.x) / (point.x - previous.x || 1);
      roof = Math.min(roof, previous.y + (point.y - previous.y) * fraction);
    }
    previous = point;
  }
  return roof;
}

// Positive z runs away from the viewer. Camera offsets never change simulation coordinates.
export function project(x: number, y: number, z: number, camera: Camera = { x: 0, y: 0 }) {
  const scale = 1 - z * 0.0022;
  return { x: OCEAN_WIDTH / 2 + (x - camera.x - OCEAN_WIDTH / 2) * scale + z * 0.32,
    y: SEA_LEVEL + (y - camera.y - SEA_LEVEL) * scale - z * 0.42, scale };
}

export function seaFloor(x: number, z = 0, beach = beaches.sandbar) { return SEA_LEVEL + bottomDepth(beach, x, z); }
