import type { Ocean } from './ocean';

type Point = { x: number; y: number };

export function drawSurfer(ctx: CanvasRenderingContext2D, ocean: Ocean, t: number) {
  const { heading, angle, stance } = ocean;
  const forward = Math.cos(heading);
  const across = Math.sin(heading);
  const swimming = ocean.state === 'submerged' || ocean.state === 'recovering';
  const standing = ocean.onFoot ? 1 : swimming ? 0 : ocean.standingBlend;
  const sizeScale = ocean.project(ocean.x, ocean.y, ocean.z).scale;
  const point = (u: number, v: number, height: number): Point => ocean.project(
    ocean.x + u * Math.cos(angle) * forward - v * across,
    ocean.y - height - u * Math.sin(angle),
    ocean.z - u * Math.cos(angle) * across - v * forward,
  );
  const pixel = (p: Point, size: number, color: string) => {
    ctx.fillStyle = color;
    const width = size * sizeScale;
    ctx.fillRect(Math.round(p.x - width / 2), Math.round(p.y - width / 2), Math.ceil(width), Math.ceil(width));
  };
  const line = (a: Point, b: Point, width: number, color: string) => {
    const steps = Math.ceil(Math.max(Math.abs(b.x - a.x), Math.abs(b.y - a.y)));
    for (let i = 0; i <= steps; i++) {
      const f = steps ? i / steps : 0;
      pixel({ x: a.x + (b.x - a.x) * f, y: a.y + (b.y - a.y) * f }, width, color);
    }
  };
  const polygon = (points: Point[], color: string) => {
    ctx.fillStyle = color;
    ctx.beginPath();
    points.forEach((p, i) => {
      if (!i) ctx.moveTo(Math.round(p.x), Math.round(p.y));
      else ctx.lineTo(Math.round(p.x), Math.round(p.y));
    });
    ctx.closePath(); ctx.fill();
  };
  const board = [[-14, 0], [-10, -3], [7, -3], [15, -1], [17, 0], [13, 2], [5, 3], [-10, 3]];
  const boardPoint = (u: number, v: number, height: number) => ocean.onFoot ? point(u, v - 4, height + 11 + u * 0.15) : point(u, v, height);
  polygon(board.map(([u, v]) => boardPoint(u, v, -1.5)), '#bc9875');
  polygon(board.map(([u, v]) => boardPoint(u, v, 0)), swimming ? '#9cceaa' : '#f3e2b7');
  line(boardPoint(9, -2, 0.5), boardPoint(9, 2, 0.5), 1.5, '#dd9276');
  line(boardPoint(-9, 0, -1), boardPoint(-10, 0, -4), 2, '#284d63');

  const foot = stance * 9;
  const stride = ocean.walking ? Math.sin(t * 9) * 2 : 0;
  const pose = (prone: [number, number, number], upright: [number, number, number]) => point(
    prone[0] + (upright[0] - prone[0]) * standing,
    prone[1] + (upright[1] - prone[1]) * standing,
    prone[2] + (upright[2] - prone[2]) * standing,
  );
  const hip = pose([-4, 0, 3], [foot, 0, 8.5]);
  const shoulder = pose([3, 0, 3.5], [foot + 0.5, 0, 16]);
  const head = pose([7, 0, 5], [foot + 1, 0, 20.5]);
  const backKnee = pose([-8, -1.2, 2], [foot - 2, -1.2, 5]);
  const frontKnee = pose([-7, 1.2, 2], [foot + 1, 1.2, 5]);
  line(pose([-12, -1.2, 1.5], [foot - 4 - stride, -1.2, 1]), backKnee, 2.5, '#c99879');
  line(backKnee, hip, 3, '#25475d');
  line(pose([-11, 1.2, 1.5], [foot + 3 + stride, 1.2, 1]), frontKnee, 2.5, '#eeb58c');
  line(frontKnee, hip, 3, '#25475d');
  polygon([pose([-4, -2.3, 3], [foot - 1, -2.3, 9]), pose([-4, 2.3, 3], [foot + 1, 2.3, 9]),
    pose([3, 2.8, 3.5], [foot + 1, 2.8, 16]), pose([3, -2.8, 3.5], [foot, -2.8, 16])], '#e97767');
  line(hip, shoulder, 4, '#e97767');
  pixel(head, 4.5, '#eeb58c');
  line({ x: head.x - 2, y: head.y - 2 }, { x: head.x + 1, y: head.y - 2 }, 2, '#263b50');
  if (across < -0.3) pixel({ x: head.x, y: head.y - 0.5 }, 4, '#263b50');
  else pixel({ x: head.x + forward * 2, y: head.y }, 1, '#745447');

  if (ocean.onFoot) {
    line(point(0, -2.8, 15), point(3, -5, 10), 2, '#c99879');
    line(point(1, 2.8, 15), point(-stride, 4, 9), 2, '#eeb58c');
  } else if (ocean.armsCrossed && standing > 0.85) {
    line(point(foot + 0.5, -2.8, 15), point(foot + 2.3, -2, 12), 2, '#c99879');
    line(point(foot + 2.3, -2, 12), point(foot + 2.5, 2.4, 13.5), 2, '#eeb58c');
    line(point(foot + 0.5, 2.8, 15), point(foot + 2.8, 2.2, 12), 2, '#eeb58c');
    line(point(foot + 2.8, 2.2, 12), point(foot + 2.5, -2.4, 14), 2, '#d7a17d');
  } else {
    const stroke = ocean.paddling || swimming ? Math.sin(t * 5) : 0;
    const backElbow = pose([1 + stroke * 2, -5, 2], [foot - 4, -5, 13]);
    const frontElbow = pose([1 - stroke * 2, 5, 2], [foot + 5, 4, 13]);
    line(pose([3, -2.8, 3.5], [foot, -2.8, 15]), backElbow, 2, '#c99879');
    line(backElbow, pose([-1 + stroke * 5, -6, stroke], [foot - 7, -5, 15]), 2, '#c99879');
    line(pose([3, 2.8, 3.5], [foot + 1, 2.8, 15]), frontElbow, 2, '#eeb58c');
    line(frontElbow, pose([-1 - stroke * 5, 6, -stroke], [foot + 8, 4, 15]), 2, '#eeb58c');
  }
}
