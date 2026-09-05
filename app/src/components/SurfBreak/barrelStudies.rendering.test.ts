import { describe, expect, it, vi } from 'vitest';
import { barrelFlowCoordinates, barrelFlowPoint, barrelLip, barrelLipContour, barrelLipImpact, barrelSample, barrelSection, curtainFlowPoint, curtainSample } from './barrelGeometry';
import { boardTrailPoint, drawBoardTrail } from './boardTrail';
import { createBarrelStudy } from './barrelStudies';
import { barrelColor, drawBarrelBack, drawBarrelFront } from './paintBarrel';
import { drawOcean } from './paintOcean';
import { drawSurferModel } from './paintSurferModel';

function recorder() {
  const rectangles: { color: string; alpha: number; x: number; y: number; w: number; h: number }[] = [];
  const stack: { fillStyle: string; globalAlpha: number }[] = [];
  const check = (...values: number[]) => {
    if (!values.every(Number.isFinite)) throw new Error('Non-finite canvas coordinates');
  };
  const context = {
    fillStyle: '', globalAlpha: 1, imageSmoothingEnabled: true,
    beginPath() {}, closePath() {}, fill() {}, clip() {},
    moveTo: check, lineTo: check, translate: check, scale: check,
    save() { stack.push({ fillStyle: this.fillStyle, globalAlpha: this.globalAlpha }); },
    restore() { Object.assign(this, stack.pop()); },
    fillRect(x: number, y: number, w: number, h: number) {
      check(x, y, w, h);
      if (w <= 0 || h <= 0) throw new Error('Empty or inverted canvas rectangle');
      rectangles.push({ color: this.fillStyle, alpha: this.globalAlpha, x, y, w, h });
    },
  };
  return { ctx: context as unknown as CanvasRenderingContext2D, rectangles, stack };
}

const frame = (treatment: 'wall' | 'hollow' | 'curtain') => createBarrelStudy(treatment).artwork.barrels[0];
const brightness = (color: string) => {
  const [r, g, b] = color.slice(1).match(/../g)!.map(value => parseInt(value, 16));
  return r * 0.2126 + g * 0.7152 + b * 0.0722;
};

describe('frozen barrel artwork', () => {
  it.each(['wall', 'hollow', 'curtain'] as const)('maps foam and texture onto the same %s surface', treatment => {
    const pose = frame(treatment);
    for (const lane of [0.22, 0.38, 0.55, 0.75, 0.95]) {
      for (const travel of [0, 0.18, 0.45, 0.75, 1]) {
        const point = barrelFlowPoint(pose, lane, travel);
        if (!barrelSample(pose, point.u, point.v).inside) continue;
        const coordinates = barrelFlowCoordinates(pose, point.u, point.v);
        expect(coordinates.lane).toBeCloseTo(lane, 2);
        expect(coordinates.travel).toBeCloseTo(travel, 2);
      }
      const crest = barrelFlowPoint(pose, lane, 0);
      const face = barrelFlowPoint(pose, lane, 0.4);
      const trough = barrelFlowPoint(pose, lane, 1);
      if (treatment === 'curtain') {
        expect(face.u).toBeCloseTo(crest.u, 5);
        expect(trough.u).toBeCloseTo(crest.u, 5);
      } else {
        const orientation = pose.peelDirection === 'right' ? -1 : 1;
        expect((face.u - crest.u) * orientation).toBeGreaterThan(0.1);
        expect((trough.u - crest.u) * orientation).toBeLessThan(-0.2);
      }
      expect(trough.v).toBeGreaterThan(1);
      expect(trough.v).toBeLessThan(1.1);
    }
  });

  it('keeps subtle flowing texture inside the darkest part of the barrel', () => {
    const pose = frame('hollow');
    const colors = Array.from({ length: 36 }, (_, i) => {
      const point = barrelFlowPoint(pose, 0.12 + i * 0.002, 0.38);
      return barrelColor(pose, point.u, point.v)!;
    });
    expect(new Set(colors).size).toBeGreaterThan(1);
    const values = colors.map(brightness);
    expect(Math.max(...values) - Math.min(...values)).toBeLessThan(25);
    expect(Math.max(...values)).toBeLessThan(90);
  });

  it('anchors the water marks to the wave when its world position and camera move together', () => {
    const pose = frame('hollow');
    const before = recorder(); const after = recorder();
    drawBarrelBack(before.ctx, pose, { x: 900, y: -28 });
    drawBarrelBack(after.ctx, { ...pose, left: pose.left + 400, floor: pose.floor + 80 }, { x: 1300, y: 52 });
    expect(after.rectangles).toEqual(before.rectangles);
  });

  it('gives the hollow a rounded roof while the long wall stays nearly level', () => {
    const wall = barrelSection(frame('wall'), 0.34);
    const hollow = barrelSection(frame('hollow'), 0.34);
    expect(wall.roof).toBeGreaterThan(-0.02);
    expect(hollow.roof).toBeLessThan(wall.roof - 0.4);
    expect(hollow.ceiling).toBeLessThan(wall.ceiling - 0.4);
  });

  it('keeps a long shaded cavity and a much brighter water crest', () => {
    const pose = frame('wall');
    for (const u of [0.18, 0.3, 0.45, 0.6, 0.72]) {
      const section = barrelSection(pose, u);
      const v = section.ceiling + (section.bottom - section.ceiling) * 0.35;
      expect(barrelSample(pose, u, v).hollow).toBe(true);
      const shadow = barrelColor(pose, u, v)!;
      const cap = barrelColor(pose, u, section.roof + 0.015)!;
      expect(brightness(cap) - brightness(shadow)).toBeGreaterThan(u < 0.6 ? 130 : 110);
    }
    expect(barrelColor(pose, 0.03, 0.6)).toBeNull();
    expect(barrelColor(pose, 0.5, -0.5)).toBeNull();
  });

  it('turns the hollow lip inward and clips the body to its continuous outer edge', () => {
    const pose = frame('hollow');
    expect(barrelLip(pose, 0.5).du).toBeLessThan(0);
    expect(barrelLip(pose, 1).du).toBeGreaterThan(0);
    expect(barrelLip(pose, 1).v).toBeGreaterThan(0.8);
    for (const t of [0.1, 0.3, 0.5]) {
      const lip = barrelLip(pose, t);
      expect(barrelSample(pose, lip.u - 0.012, lip.v).inside).toBe(false);
    }
  });

  it('lights the hollow gradually toward the face and reflected water below', () => {
    const pose = frame('hollow');
    const section = barrelSection(pose, 0.4);
    const colors = Array.from({ length: 24 }, (_, i) => barrelColor(pose, 0.4,
      section.ceiling + (section.bottom - section.ceiling) * (0.3 + i / 24 * 0.69))!);
    expect(new Set(colors).size).toBeGreaterThan(7);
    expect(brightness(colors[colors.length - 1])).toBeGreaterThan(brightness(colors[0]) + 50);
  });

  it('creates C from one lip that stays overhead before falling on the left', () => {
    const wallStudy = createBarrelStudy('wall');
    const curtainStudy = createBarrelStudy('curtain');
    const wall = wallStudy.artwork.barrels[0];
    const curtain = curtainStudy.artwork.barrels[0];
    expect(wall.curlSide).toBe('left');
    expect(frame('hollow').curlSide).toBe('left');
    expect(curtain.curlSide).toBe('left');
    expect(wall.peelDirection).toBe('left');
    expect(curtain.peelDirection).toBe('right');
    expect(wallStudy.ocean.heading).toBe(Math.PI);
    expect(curtainStudy.ocean.heading).toBe(0);
    expect(curtain.left).toBe(wall.left);
    expect(curtainStudy.ocean.x).toBe(wallStudy.ocean.x);
    const lipRoot = barrelLip(curtain, 0);
    const fallingLip = barrelLip(curtain, 1);
    const rider = (curtainStudy.ocean.x - curtain.left) / curtain.width;
    expect(fallingLip.u).toBeLessThan(rider - 0.3);
    expect(lipRoot.u).toBeGreaterThan(rider + 0.6);
    expect(fallingLip.v).toBeGreaterThan(lipRoot.v + 0.75);
    for (const t of Array.from({ length: 41 }, (_, i) => i / 40)) {
      const lip = barrelLip(curtain, t);
      expect(barrelSection(curtain, lip.u).roof).toBeCloseTo(lip.v, 3);
      const inner = barrelLipContour(curtain, t, 0.6);
      expect(inner.v).toBeGreaterThanOrEqual(lip.v);
      expect(inner.v).toBeLessThanOrEqual(barrelSection(curtain, lip.u).bottom);
      if (t > 0.05 && t < 0.68) {
        expect(barrelSample(curtain, lip.u, lip.v - 0.03).inside).toBe(false);
      } else if (t > 0.75 && t < 0.95) {
        expect(barrelSample(curtain, lip.u - 0.05, lip.v).inside).toBe(false);
      }
    }
    const impact = barrelLipImpact(curtain);
    expect(impact.u).toBeCloseTo(fallingLip.u, 5);
    expect(impact.v).toBeGreaterThan(fallingLip.v + 0.15);

    const cavityArea = (pose: typeof curtain) => {
      const root = barrelLip(pose, 0).u;
      const tip = barrelLip(pose, 1).u;
      return Array.from({ length: 101 }, (_, i) => {
        const u = i / 100 * 1.1;
        if (u < tip || u > root) return 0;
        const section = barrelSection(pose, u);
        return Math.max(0, section.bottom - section.ceiling);
      }).reduce((sum, height) => sum + height, 0);
    };
    expect(cavityArea(curtain)).toBeGreaterThan(cavityArea({ ...curtain, lipFall: 0.2 }) * 3);
    const canvas = recorder();
    drawSurferModel(canvas.ctx, curtainStudy.ocean, 0, 1.8);
    const wake = canvas.rectangles.filter(r => r.color === '#e9ffe7');
    const tail = curtainStudy.ocean.project(curtainStudy.ocean.x, curtainStudy.ocean.y).x - 17 * 1.8;
    expect(Math.min(...wake.map(r => r.x))).toBeLessThan(tail - 100);
  });

  it('keeps the near sheet attached to the falling part of that lip', () => {
    const pose = frame('curtain');
    for (const across of [0.05, 0.5, 0.95]) {
      for (const travel of [0.05, 0.25, 0.5, 0.75, 0.95]) {
        const point = curtainFlowPoint(pose, across, travel);
        const sample = curtainSample(pose, point.u, point.v);
        expect(sample.across).toBeCloseTo(across, 1);
        expect(sample.travel).toBeCloseTo(travel, 1);
        expect(sample.inside).toBe(true);
        if (across === 0.5) {
          const lip = barrelLip(pose, 0.72 + travel * 0.28);
          expect(point.u).toBeCloseTo(lip.u, 5);
          expect(point.v).toBeCloseTo(lip.v, 5);
        }
      }
    }
    expect(curtainSample(pose, 0.5, -1).inside).toBe(false);
    expect(curtainSample(pose, 0.5, 1.2).inside).toBe(false);
  });

  it('places the translucent curtain in front of the rider and restores its drawing state', () => {
    const study = createBarrelStudy('curtain');
    const pose = study.artwork.barrels[0];
    const sheet = curtainFlowPoint(pose, 0.5, 0.6);
    expect(curtainSample(pose, sheet.u, sheet.v).inside).toBe(true);
    const canvas = recorder();
    drawBarrelFront(canvas.ctx, pose, { x: 900, y: -28 });
    expect(canvas.rectangles.some(r => r.alpha === 0.34)).toBe(true);
    expect(canvas.rectangles.every(r => r.alpha > 0 && r.alpha < 1)).toBe(true);
    expect(canvas.stack).toHaveLength(0);
    expect(canvas.ctx.globalAlpha).toBe(1);
  });

  it.each(['wall', 'hollow'] as const)('keeps the surfer unobscured in %s', treatment => {
    const canvas = recorder();
    drawBarrelFront(canvas.ctx, frame(treatment), { x: 900, y: -28 });
    expect(canvas.rectangles).toHaveLength(0);
  });

  it.each(['wall', 'hollow', 'curtain'] as const)('renders %s deterministically without advancing the ocean', treatment => {
    const study = createBarrelStudy(treatment);
    const step = vi.spyOn(study.ocean, 'step');
    const before = JSON.stringify(study);
    const first = recorder(); const second = recorder();
    drawOcean(first.ctx, study.ocean, true, study.artwork);
    drawOcean(second.ctx, study.ocean, true, study.artwork);
    expect(first.rectangles).toEqual(second.rectangles);
    expect(JSON.stringify(study)).toBe(before);
    expect(step).not.toHaveBeenCalled();
    expect(first.rectangles.some(r => r.color === '#e97767')).toBe(true);
    expect(first.rectangles.some(r => r.color === '#0d3959')).toBe(true);
    expect(first.stack).toHaveLength(0);
    expect(first.ctx.imageSmoothingEnabled).toBe(false);
  });

  it('draws rear water, the posed surfer, then the translucent near water', () => {
    const study = createBarrelStudy('curtain');
    const canvas = recorder();
    drawOcean(canvas.ctx, study.ocean, true, study.artwork);
    const shadow = canvas.rectangles.findIndex(r => r.color === '#0d3959');
    const shirt = canvas.rectangles.findIndex(r => r.color === '#e97767');
    const curtain = canvas.rectangles.findIndex(r => r.alpha === 0.34);
    expect(shadow).toBeGreaterThan(-1);
    expect(shirt).toBeGreaterThan(shadow);
    expect(curtain).toBeGreaterThan(shirt);
  });

  it('does not activate the experimental artwork for normal game drawing', () => {
    const { ocean } = createBarrelStudy('wall');
    const canvas = recorder();
    drawOcean(canvas.ctx, ocean, true);
    expect(canvas.rectangles.some(r => r.color === '#0d3959')).toBe(false);
    expect(canvas.rectangles.some(r => r.color === '#e97767')).toBe(true);
  });

  it('keeps its pixels aligned and drawing bounded when the artwork fills the viewport', () => {
    const canvas = recorder();
    drawBarrelBack(canvas.ctx, frame('hollow'), { x: 900, y: -28 });
    expect(canvas.rectangles.length).toBeLessThan(14000);
    expect(canvas.rectangles.every(r => [r.x, r.y, r.w, r.h].every(n => n % 2 === 0))).toBe(true);
    expect(canvas.stack).toHaveLength(0);
  });
});

describe('projected study surfer', () => {
  function body(heading: number, standing: number, depth = 0) {
    const { ocean } = createBarrelStudy('wall');
    ocean.heading = heading; ocean.standingBlend = standing; ocean.z = depth;
    const canvas = recorder();
    drawSurferModel(canvas.ctx, ocean, 0, 1.8);
    const shirt = canvas.rectangles.filter(r => ['#e97767', '#ef866d', '#c56355'].includes(r.color));
    const top = Math.min(...shirt.map(r => r.y));
    const bottom = Math.max(...shirt.map(r => r.y + r.h));
    const left = Math.min(...shirt.map(r => r.x));
    const right = Math.max(...shirt.map(r => r.x + r.w));
    return { canvas, height: bottom - top, width: right - left, top, left, right, ocean };
  }

  it('has an inclined, full torso and blends down onto the board for paddling', () => {
    const upright = body(Math.PI, 1);
    const prone = body(Math.PI, 0);
    const halfway = body(Math.PI, 0.5);
    expect(upright.width).toBeGreaterThan(14);
    expect(upright.height).toBeGreaterThan(16);
    expect(prone.height).toBeLessThan(7);
    expect(halfway.height).toBeGreaterThan(prone.height);
    expect(halfway.height).toBeLessThan(upright.height);
    expect(upright.top).toBeLessThan(prone.top - 20);
  });

  it('leans toward either heading and keeps body volume when viewed along the board', () => {
    const left = body(Math.PI, 1);
    const right = body(0, 1);
    expect(left.left).toBeLessThan(440 - 10);
    expect(right.right).toBeGreaterThan(440 + 10);
    for (const heading of [Math.PI / 2, -Math.PI / 2]) {
      const front = body(heading, 1);
      expect(front.width).toBeGreaterThan(7);
      expect(front.height).toBeGreaterThan(15);
    }
  });

  it('shrinks in depth and renders crossed arms without changing the ocean pose', () => {
    const near = body(Math.PI, 1);
    const far = body(Math.PI, 1, 80);
    expect(far.height).toBeLessThan(near.height);
    near.ocean.armsCrossed = true;
    const before = JSON.stringify(near.ocean);
    const crossed = recorder();
    drawSurferModel(crossed.ctx, near.ocean, 0, 1.8);
    expect(crossed.rectangles).not.toEqual(near.canvas.rectangles);
    expect(JSON.stringify(near.ocean)).toBe(before);
    expect(crossed.rectangles.every(r => [r.x, r.y, r.w, r.h].every(Number.isInteger))).toBe(true);
  });

  it('starts the wake at the tail and curves away without a kink at the board', () => {
    expect(boardTrailPoint(0)).toEqual([-17, 0, 0]);
    const next = boardTrailPoint(0.001);
    expect(next[0]).toBeLessThan(-17);
    expect(Math.abs(next[2] / (next[0] + 17))).toBeLessThan(0.001);
    const middle = boardTrailPoint(0.5);
    const end = boardTrailPoint(1);
    expect(end[0]).toBeLessThan(middle[0]);
    expect(end[2]).toBeGreaterThan(middle[2] * 3);
  });

  it('fades the connected trail into broken foam and preserves canvas opacity', () => {
    const canvas = recorder();
    canvas.ctx.globalAlpha = 0.7;
    drawBoardTrail(canvas.ctx, ([u, v, h]) => ({ x: u, y: v - h }), 1);
    const core = canvas.rectangles.filter(r => r.color === '#e9ffe7');
    const tail = core[core.length - 1];
    expect(tail.alpha).toBeCloseTo(0.7 * 0.95);
    expect(core[0].alpha).toBeLessThan(tail.alpha * 0.3);
    expect(core.filter(r => r.x < -90).length).toBeLessThan(core.filter(r => r.x > -60).length);
    expect(canvas.rectangles.every(r => [r.x, r.y, r.w, r.h].every(n => n % 2 === 0))).toBe(true);
    expect(canvas.ctx.globalAlpha).toBe(0.7);
    expect(canvas.stack).toHaveLength(0);
  });

  it.each([0, Math.PI])('projects the trail behind the board at heading %s', heading => {
    const { canvas, ocean } = body(heading, 1);
    const wake = canvas.rectangles.filter(r => r.color === '#e9ffe7');
    const origin = ocean.project(ocean.x, ocean.y, ocean.z);
    const tail = origin.x - Math.cos(heading) * 17 * 1.8;
    expect(wake.length).toBeGreaterThan(50);
    expect(wake[wake.length - 1].x).toBeGreaterThanOrEqual(tail - 2);
    expect(wake[wake.length - 1].x).toBeLessThanOrEqual(tail);
    expect((wake[0].x - tail) * Math.cos(heading)).toBeLessThan(-100);
  });

  it('projects the wake into depth with the board and suppresses it while carried or submerged', () => {
    const left = body(Math.PI, 1);
    const away = body(Math.PI / 2, 1, 60);
    const wake = away.canvas.rectangles.filter(r => r.color === '#e9ffe7');
    expect(wake).not.toEqual(left.canvas.rectangles.filter(r => r.color === '#e9ffe7'));
    for (const pose of ['walking', 'recovering'] as const) {
      left.ocean.onFoot = pose === 'walking';
      left.ocean.recovery = pose === 'recovering' ? 1 : 0;
      const canvas = recorder();
      drawSurferModel(canvas.ctx, left.ocean, 0, 1.8);
      expect(canvas.rectangles.some(r => r.color === '#e9ffe7')).toBe(false);
    }
  });
});
