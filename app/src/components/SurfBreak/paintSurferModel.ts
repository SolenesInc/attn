import type { Ocean } from './ocean';
import { drawBoardTrail } from './boardTrail';

type Joint = [number, number, number];
type Point = { x: number; y: number };

// The study model is projected from board-local joints, including heading and rail tilt.
export function drawSurferModel(ctx: CanvasRenderingContext2D, ocean: Ocean, t: number, scale = 1) {
  const anchor = ocean.project(ocean.x, ocean.y, ocean.z);
  const forward = Math.cos(ocean.heading);
  const across = Math.sin(ocean.heading);
  const submerged = ocean.state === 'submerged' || ocean.state === 'recovering';
  const standing = ocean.onFoot ? 1 : submerged ? 0 : ocean.standingBlend;
  const foot = ocean.stance * 7;
  const stroke = ocean.paddling || submerged ? Math.sin(t * 5) : 0;
  const pixel = 1;
  const project = ([u, v, h]: Joint): Point => {
    const p = ocean.project(
      ocean.x + u * Math.cos(ocean.angle) * forward - v * across,
      ocean.y - h - u * Math.sin(ocean.angle),
      ocean.z - u * Math.cos(ocean.angle) * across - v * forward,
    );
    return { x: anchor.x + (p.x - anchor.x) * scale, y: anchor.y + (p.y - anchor.y) * scale };
  };
  const posed = (upright: Joint, prone: Joint): Point => project(upright.map((value, i) =>
    prone[i] + (value + (i === 0 ? foot : 0) - prone[i]) * standing) as Joint);
  const polygon = (points: Point[], color: string) => {
    ctx.fillStyle = color;
    const top = Math.floor(Math.min(...points.map(p => p.y)) / pixel) * pixel;
    const bottom = Math.ceil(Math.max(...points.map(p => p.y)) / pixel) * pixel;
    for (let y = top; y < bottom; y += pixel) {
      const crossings: number[] = [];
      for (let i = 0; i < points.length; i++) {
        const a = points[i]; const b = points[(i + 1) % points.length];
        if ((a.y <= y + pixel / 2) !== (b.y <= y + pixel / 2)) {
          crossings.push(a.x + (y + pixel / 2 - a.y) * (b.x - a.x) / (b.y - a.y));
        }
      }
      crossings.sort((a, b) => a - b);
      for (let i = 0; i + 1 < crossings.length; i += 2) {
        const x = Math.round(crossings[i] / pixel) * pixel;
        const width = Math.round(crossings[i + 1] / pixel) * pixel - x;
        if (width > 0) ctx.fillRect(x, y, width, pixel);
      }
    }
  };
  const limb = (a: Point, b: Point, start: number, end: number, color: string) => {
    const length = Math.hypot(b.x - a.x, b.y - a.y) || 1;
    const nx = (b.y - a.y) / length * scale * anchor.scale / 2;
    const ny = (a.x - b.x) / length * scale * anchor.scale / 2;
    polygon([{ x: a.x + nx * start, y: a.y + ny * start }, { x: b.x + nx * end, y: b.y + ny * end },
      { x: b.x - nx * end, y: b.y - ny * end }, { x: a.x - nx * start, y: a.y - ny * start }], color);
  };
  const mesh = (upright: Joint[], prone: Joint[], color: string) => polygon(upright.map((p, i) => posed(p, prone[i])), color);
  if (!ocean.onFoot && !submerged) {
    const shadow = project([0, 0, -3]);
    ctx.fillStyle = '#176a7c';
    ctx.fillRect(Math.round(shadow.x - 20 * scale), Math.round(shadow.y), Math.round(39 * scale), 2);
    drawBoardTrail(ctx, project, standing);
  }
  const deck: Joint[] = [[-17, 0, 0], [-13, -3, 0], [10, -3, 0], [17, -1, 0.6], [19, 0, 1], [15, 2, 0], [8, 3, 0], [-13, 3, 0]];
  const boardPoint = (p: Joint): Point => project(ocean.onFoot ? [p[0], p[1] - 5, p[2] + 12 + p[0] * 0.12] : p);
  polygon([[-12, 0, -1], [-9, 0, -1], [-13, 0, -5]].map(p => boardPoint(p as Joint)), '#23485c');
  polygon(deck.map(([u, v, h]) => boardPoint([u, v, h - 1.6])), '#bc9875');
  polygon(deck.map(boardPoint), '#f3e2b7');
  limb(boardPoint([-14, 0, 0.5]), boardPoint([16, 0, 1]), 1.1, 0.7, '#fff5d2');
  limb(boardPoint([10, -2, 0.8]), boardPoint([10, 2, 0.8]), 1.5, 1.5, '#d17c69');

  const hip = posed([-1, 0, 10], [-4, 0, 3.5]);
  const rearKnee = posed([-6, -1.6, 5.8], [-8, -1.6, 2.4]);
  const leadKnee = posed([9, 1.6, 6.5], [-7, 1.6, 2.4]);
  const rearAnkle = posed([-11, -1.6, 1], [-13, -1.6, 1.5]);
  const leadAnkle = posed([8, 1.6, 1], [-12, 1.6, 1.5]);
  limb(hip, rearKnee, 5, 3.5, '#1f344d');
  limb(rearKnee, rearAnkle, 3.1, 2, '#b47759');
  limb(rearAnkle, posed([-8, -1.6, 0.8], [-15, -1.6, 1.5]), 2.2, 1.7, '#d9976d');
  limb(hip, leadKnee, 5.6, 4.1, '#284965');
  limb(leadKnee, leadAnkle, 3.3, 2.1, '#edb184');
  limb(leadAnkle, posed([12, 1.6, 0.8], [-14, 1.6, 1.5]), 2.2, 1.7, '#f4c699');

  const rearShoulder = posed([4, -2.6, 18.5], [3, -2.6, 3.8]);
  const leadShoulder = posed([5, 2.6, 18.8], [3, 2.6, 3.8]);
  const rearElbow = posed([-2, -4, 17], [stroke * 2, -5, 2]);
  const rearHand = posed([-9, -4, 13.5], [-2 + stroke * 5, -6, stroke]);
  if (!ocean.armsCrossed || standing < 0.85) {
    limb(rearShoulder, rearElbow, 3.6, 2.7, '#b96453');
    limb(rearElbow, rearHand, 2.5, 1.6, '#cf8d66');
  }

  mesh([[-4, 0, 10], [2, 0, 9], [8, 0, 18], [5, 0, 21], [1, 0, 20]],
    [[-5, -2.7, 3], [-5, 2.7, 3], [4, 2.8, 4], [5, 0, 4.2], [3, -2.8, 4]], '#b85c53');
  mesh([[-2, 1, 11], [2, 1, 10], [8, 1, 18], [5, 1, 20], [2, 1, 19]],
    [[-4, -1.6, 3.5], [-4, 2.8, 3.5], [4, 2.8, 4.3], [5, 0, 4.5], [3, -1.6, 4.3]], '#e97767');
  mesh([[2, -2, 10], [2, 2, 10], [7, 2.8, 19], [7, -2.8, 19]],
    [[-4, -2.5, 3.2], [-4, 2.5, 3.2], [4, 2.8, 4], [4, -2.8, 4]], across > 0 ? '#ef866d' : '#c56355');
  limb(posed([3, 1, 19.6], [2, 1.5, 4.4]), posed([6.5, 1, 18.7], [4, 1.5, 4.5]), 1.4, 1.3, '#f99478');
  limb(posed([-3, 0, 10], [-5, 0, 3]), posed([2, 0, 9.5], [-3, 0, 3]), 2.2, 2.2, '#213d56');

  limb(posed([5, 0, 20], [4.5, 0, 4]), posed([6, 0, 22], [6, 0, 5]), 3, 2.8, '#cf8d66');
  mesh([[4, 0, 22], [4, 0, 26.3], [8, 0, 26.5], [9, 0, 24], [10, 0, 23.7], [9, 0, 22.6], [8, 0, 21.7]],
    [[5, 0, 3.7], [5, 0, 7.5], [9, 0, 7.5], [10, 0, 5.8], [11, 0, 5.3], [10, 0, 4.4], [8, 0, 3.7]], '#edb184');
  mesh([[3.5, 0.2, 23.5], [3, 0.2, 26.5], [5, 0.2, 27.5], [8.5, 0.2, 27], [9, 0.2, 25.4], [5.4, 0.2, 25.4], [5.5, 0.2, 23]],
    [[5, 0.2, 5], [4, 0.2, 7], [6, 0.2, 8.4], [9, 0.2, 8], [10, 0.2, 6.7], [6.6, 0.2, 6.7], [6.5, 0.2, 4.8]], '#293345');
  mesh([[7, -2, 22], [7, 2, 22], [7, 2, 25.5], [7, -2, 25.5]],
    [[8, -2, 4], [8, 2, 4], [8, 2, 7], [8, -2, 7]], across > 0 ? '#edb184' : '#293345');
  limb(posed([7, -2.2, 26], [8, -2.2, 7.6]), posed([7, 2.2, 26], [8, 2.2, 7.6]), 1.5, 1.5, '#293345');
  if (across < -0.3) {
    limb(posed([5, 0.3, 22.5], [6, 0.3, 4.7]), posed([5.5, 0.3, 25.8], [6, 0.3, 7]), 4, 4, '#293345');
  } else {
    limb(posed([8, 0.4, 24.2], [9, 0.4, 6]), posed([8.6, 0.4, 24.2], [9.6, 0.4, 6]), 0.7, 0.7, '#594636');
  }

  if (ocean.armsCrossed && standing > 0.85) {
    const rear = posed([4, -1, 15], [0, 0, 0]);
    const lead = posed([7, 2, 15], [0, 0, 0]);
    limb(rearShoulder, rear, 3, 2.3, '#d9976d');
    limb(rear, posed([7, 2.6, 17], [0, 0, 0]), 2.5, 1.6, '#edb184');
    limb(leadShoulder, lead, 3.4, 2.6, '#e97767');
    limb(lead, posed([3, -1.6, 17], [0, 0, 0]), 2.6, 1.8, '#f4c699');
  } else {
    const elbow = posed([11, 3.5, 15.6], [1 - stroke * 2, 5, 2]);
    limb(leadShoulder, posed([8, 3, 17], [2.5, 3.5, 3]), 4, 3.4, '#f0886d');
    limb(posed([8, 3, 17], [2.5, 3.5, 3]), elbow, 2.8, 2.2, '#edb184');
    limb(elbow, posed([17, 3.5, 13.8], [-2 - stroke * 5, 6, -stroke]), 2.3, 1.5, '#f4c699');
  }
}
