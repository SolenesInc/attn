import { clamp, ease } from './water';

export type BarrelTreatment = 'wall' | 'hollow' | 'curtain';
export type BarrelCurlSide = 'left' | 'right';
export type BarrelPeelDirection = 'left' | 'right';
export type BarrelFrame = {
  treatment: BarrelTreatment;
  left: number; floor: number; width: number; height: number;
  roundness: number; curtain: number; seed: number;
  lipFall: number;
  curlSide: BarrelCurlSide; peelDirection: BarrelPeelDirection;
};

const barrelDomain = 1.15;
type BarrelPoint = { u: number; v: number; du: number; dv: number };
type BarrelSection = { roof: number; ceiling: number; bottom: number };

export function barrelDisplayU(frame: BarrelFrame, localU: number) {
  return frame.curlSide === 'right' ? barrelDomain - localU : localU;
}

function barrelLocalU(frame: BarrelFrame, displayU: number) {
  return frame.curlSide === 'right' ? barrelDomain - displayU : displayU;
}

function barrelSectionAt(frame: BarrelFrame, u: number): BarrelSection {
  if (frame.treatment === 'curtain') return lipDrivenSectionAt(frame, u);
  const roof = 0.32 * Math.exp(-Math.max(0, u) / 0.043)
    - frame.roundness * 0.43 * Math.exp(-(((u - 0.34) / 0.34) ** 2))
    + Math.sin(u * 8) * 0.008;
  const ceiling = roof + 0.19 + Math.sin(u * 5) * 0.015;
  const bottom = 1.02 - 0.93 * ease((u - 0.34) / 0.8) ** 1.65;
  return { roof, ceiling, bottom };
}

function lipDrivenSectionAt(frame: BarrelFrame, u: number): BarrelSection {
  const root = barrelLipAt(frame, 0);
  const tip = barrelLipAt(frame, 1);
  const span = Math.max(0.001, root.u - tip.u);
  if (u <= tip.u) {
    return { roof: tip.v, ceiling: tip.v + 0.08, bottom: 1.04 };
  }
  if (u >= root.u) {
    const shoulder = ease((u - root.u) / Math.max(0.001, barrelDomain - root.u));
    const roof = root.v + shoulder * 0.045;
    return { roof, ceiling: roof + 0.09, bottom: roof + 0.13 + shoulder * 0.08 };
  }
  const lip = barrelLipAtU(frame, u);
  const across = clamp((u - tip.u) / span, 0, 1);
  const closingFace = ease(across) ** 1.45;
  const bottom = 1.04 + (root.v + 0.105 - 1.04) * closingFace;
  const thickness = 0.055 + 0.045 * Math.sin(Math.PI * across);
  return { roof: lip.v, ceiling: lip.v + thickness, bottom: Math.max(lip.v + thickness + 0.02, bottom) };
}

export function barrelSection(frame: BarrelFrame, u: number) {
  return barrelSectionAt(frame, barrelLocalU(frame, u));
}

export function barrelSample(frame: BarrelFrame, u: number, v: number) {
  const localU = barrelLocalU(frame, u);
  const { roof, ceiling, bottom } = barrelSectionAt(frame, localU);
  const mouth = frame.treatment === 'curtain' ? 0 : v > 0.31 && v < 1.05
    ? 0.125 * Math.sin(Math.PI * (v - 0.31) / 0.74) ** 0.8 : 0;
  const inside = localU >= Math.max(mouth, barrelFront(frame, v))
    && localU <= barrelDomain && v >= roof && v <= 1.14;
  const depth = (v - ceiling) / Math.max(0.04, bottom - ceiling);
  const shelter = frame.treatment === 'curtain'
    ? lipDrivenShelter(frame, localU, v, roof, bottom)
    : (1 - ease((localU - 0.5) / 0.36)) * ease((v - roof - 0.09) / 0.1);
  return { inside, localU, roof, ceiling, bottom, depth, shelter,
    hollow: inside && depth >= 0 && depth <= 1 && shelter > 0.18 };
}

function lipDrivenShelter(frame: BarrelFrame, u: number, v: number, roof: number, bottom: number) {
  const root = barrelLipAt(frame, 0);
  const tip = barrelLipAt(frame, 1);
  const across = clamp((u - tip.u) / Math.max(0.001, root.u - tip.u), 0, 1);
  const underLip = ease((v - roof - 0.035) / Math.max(0.04, (bottom - roof) * 0.2));
  return Math.sin(Math.PI * across) ** 0.35 * underLip;
}

function barrelFront(frame: BarrelFrame, v: number) {
  if (frame.treatment === 'curtain') {
    const root = barrelLipAt(frame, 0.72);
    const tip = barrelLipAt(frame, 1);
    if (v < root.v) return root.u;
    if (v > tip.v) return tip.u;
    let low = 0.72; let high = 1;
    for (let i = 0; i < 14; i++) {
      const mid = (low + high) / 2;
      if (barrelLipAt(frame, mid).v < v) low = mid;
      else high = mid;
    }
    return barrelLipAt(frame, (low + high) / 2).u;
  }
  if (v < barrelLipAt(frame, 0).v || v > barrelLipAt(frame, 1).v) return 0;
  let low = 0; let high = 1;
  for (let i = 0; i < 10; i++) {
    const mid = (low + high) / 2;
    if (barrelLipAt(frame, mid).v < v) low = mid;
    else high = mid;
  }
  return barrelLipAt(frame, (low + high) / 2).u;
}

function barrelLipAt(frame: BarrelFrame, t: number): BarrelPoint {
  if (frame.treatment === 'curtain') return fallingLipAt(frame, t);
  const root = 0.15 + frame.roundness * 0.18;
  const roof = barrelSectionAt(frame, root).roof;
  const drop = 0.46 + frame.roundness * 0.39;
  const a = { u: root, v: roof };
  const b = { u: 0.075, v: roof + 0.035 };
  const c = { u: -0.055 * frame.roundness, v: drop - 0.28 };
  const d = { u: 0.022 + frame.roundness * 0.012, v: drop };
  const s = 1 - t;
  return {
    u: s ** 3 * a.u + 3 * s * s * t * b.u + 3 * s * t * t * c.u + t ** 3 * d.u,
    v: s ** 3 * a.v + 3 * s * s * t * b.v + 3 * s * t * t * c.v + t ** 3 * d.v,
    du: 3 * s * s * (b.u - a.u) + 6 * s * t * (c.u - b.u) + 3 * t * t * (d.u - c.u),
    dv: 3 * s * s * (b.v - a.v) + 6 * s * t * (c.v - b.v) + 3 * t * t * (d.v - c.v),
  };
}

function fallingLipAt(frame: BarrelFrame, t: number): BarrelPoint {
  const fall = clamp(frame.lipFall, 0, 1);
  const roofEnd = { u: 0.90 - fall * 0.75, v: 0.05 };
  const split = 0.72;
  if (t <= split) {
    const p = cubic(t / split,
      { u: 1.02, v: 0.02 },
      { u: 0.94 - fall * 0.22, v: 0.02 - fall * 0.05 },
      { u: 0.92 - fall * 0.60, v: 0.03 - fall * 0.07 },
      roofEnd);
    return { ...p, du: p.du / split, dv: p.dv / split };
  }
  const p = cubic((t - split) / (1 - split),
    roofEnd,
    { u: 0.90 - fall * 0.83, v: 0.06 + fall * 0.03 },
    { u: 0.90 - fall * 0.855, v: 0.11 + fall * 0.44 },
    { u: 0.90 - fall * 0.865, v: 0.15 + fall * 0.70 });
  return { ...p, du: p.du / (1 - split), dv: p.dv / (1 - split) };
}

function cubic(t: number, a: { u: number; v: number }, b: { u: number; v: number },
  c: { u: number; v: number }, d: { u: number; v: number }): BarrelPoint {
  const s = 1 - t;
  return {
    u: s ** 3 * a.u + 3 * s * s * t * b.u + 3 * s * t * t * c.u + t ** 3 * d.u,
    v: s ** 3 * a.v + 3 * s * s * t * b.v + 3 * s * t * t * c.v + t ** 3 * d.v,
    du: 3 * s * s * (b.u - a.u) + 6 * s * t * (c.u - b.u) + 3 * t * t * (d.u - c.u),
    dv: 3 * s * s * (b.v - a.v) + 6 * s * t * (c.v - b.v) + 3 * t * t * (d.v - c.v),
  };
}

function barrelLipAtU(frame: BarrelFrame, u: number): BarrelPoint {
  const root = barrelLipAt(frame, 0);
  const tip = barrelLipAt(frame, 1);
  if (u >= root.u) return root;
  if (u <= tip.u) return tip;
  let low = 0; let high = 1;
  for (let i = 0; i < 16; i++) {
    const mid = (low + high) / 2;
    if (barrelLipAt(frame, mid).u > u) low = mid;
    else high = mid;
  }
  return barrelLipAt(frame, (low + high) / 2);
}

export function barrelLip(frame: BarrelFrame, t: number) {
  const lip = barrelLipAt(frame, t);
  const orientation = frame.curlSide === 'right' ? -1 : 1;
  return { ...lip, u: barrelDisplayU(frame, lip.u), du: lip.du * orientation };
}

function flowBend(frame: BarrelFrame, travel: number) {
  if (frame.treatment === 'curtain') return 0;
  const bend = (0.17 + frame.roundness * 0.04) * Math.sin(Math.PI * travel)
    - 0.24 * travel * travel;
  const peel = frame.peelDirection === 'right' ? -1 : 1;
  const curl = frame.curlSide === 'right' ? -1 : 1;
  return bend * peel * curl;
}

export function barrelLipContour(frame: BarrelFrame, t: number, inset: number) {
  const lip = barrelLip(frame, t);
  const section = barrelSection(frame, lip.u);
  const taper = Math.sin(Math.PI * clamp(t, 0, 1)) ** 0.6;
  const depth = Math.min(0.18, Math.max(0, section.bottom - section.roof) * 0.34)
    * clamp(inset, 0, 1) * taper;
  return { u: lip.u, v: lip.v + depth };
}

export function barrelLipImpact(frame: BarrelFrame) {
  const tip = barrelLip(frame, 1);
  return { u: tip.u, v: barrelSection(frame, tip.u).bottom };
}

// Foam paths and water texels share this map from the crest around to the trough.
export function barrelFlowPoint(frame: BarrelFrame, lane: number, travel: number) {
  const point = barrelFlowPointAt(frame, lane, travel);
  return { u: barrelDisplayU(frame, point.u), v: point.v };
}

export function barrelFlowCoordinates(frame: BarrelFrame, u: number, v: number) {
  const localU = barrelLocalU(frame, u);
  const { roof } = barrelSectionAt(frame, localU);
  let travel = (v - roof) / (1.04 - roof);
  if (travel > 0 && travel < 1) {
    let low = 0; let high = 1;
    for (let i = 0; i < 10; i++) {
      travel = (low + high) / 2;
      const point = barrelFlowPointAt(frame, localU - flowBend(frame, travel), travel);
      if (point.v < v) low = travel;
      else high = travel;
    }
    travel = (low + high) / 2;
  }
  return { lane: localU - flowBend(frame, travel), travel };
}

function barrelFlowPointAt(frame: BarrelFrame, lane: number, travel: number) {
  const u = lane + flowBend(frame, travel);
  const roof = barrelSectionAt(frame, lane).roof;
  return { u, v: roof + travel * (1.04 - roof) };
}

function curtainLanes(frame: BarrelFrame) {
  if (frame.treatment === 'curtain') return { left: 0, width: 1 };
  const left = barrelLipAt(frame, 0).u + 0.12;
  return { left, width: 0.34 };
}

export function curtainFlowPoint(frame: BarrelFrame, across: number, travel: number) {
  if (frame.treatment === 'curtain') {
    const t = 0.72 + clamp(travel, 0, 1) * 0.28;
    const lip = barrelLip(frame, t);
    const length = Math.hypot(lip.du * frame.width, lip.dv * frame.height) || 1;
    const orientation = frame.curlSide === 'right' ? -1 : 1;
    const offset = (clamp(across, 0, 1) - 0.5) * 2 * frame.height * (0.04 + frame.lipFall * 0.055);
    const nx = orientation * lip.dv * frame.height / length;
    const ny = -orientation * lip.du * frame.width / length;
    return {
      u: lip.u + nx * offset / frame.width,
      v: lip.v + ny * offset / frame.height,
    };
  }
  const { left, width } = curtainLanes(frame);
  return barrelFlowPoint(frame, left + across * width, travel);
}

export function curtainSample(frame: BarrelFrame, u: number, v: number) {
  if (frame.treatment === 'curtain') {
    let closest = { distance: Infinity, travel: 0, across: 0 };
    for (let i = 0; i <= 80; i++) {
      const travel = i / 80;
      const center = curtainFlowPoint(frame, 0.5, travel);
      const edge = curtainFlowPoint(frame, 1, travel);
      const radius = Math.hypot((edge.u - center.u) * frame.width, (edge.v - center.v) * frame.height) || 1;
      const distance = Math.hypot((u - center.u) * frame.width, (v - center.v) * frame.height);
      if (distance >= closest.distance) continue;
      const lip = barrelLip(frame, 0.72 + travel * 0.28);
      const length = Math.hypot(lip.du * frame.width, lip.dv * frame.height) || 1;
      const nx = (frame.curlSide === 'right' ? -1 : 1) * lip.dv * frame.height / length;
      const ny = -(frame.curlSide === 'right' ? -1 : 1) * lip.du * frame.width / length;
      const projection = ((u - center.u) * frame.width * nx + (v - center.v) * frame.height * ny) / radius;
      closest = { distance, travel, across: 0.5 + projection * 0.5 };
    }
    return { lane: closest.travel, travel: closest.travel, across: closest.across,
      inside: frame.curtain > 0 && closest.distance <= frame.height * (0.04 + frame.lipFall * 0.055) };
  }
  const { lane, travel } = barrelFlowCoordinates(frame, u, v);
  const { left, width } = curtainLanes(frame);
  const across = (lane - left) / width;
  return { lane, travel, across,
    inside: frame.curtain > 0 && across >= 0 && across <= 1 && travel >= 0 && travel <= 1
      && barrelSample(frame, u, v).inside };
}
