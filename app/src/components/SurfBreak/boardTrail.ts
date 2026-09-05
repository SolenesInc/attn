import { noise } from './water';

type Joint = [number, number, number];
type Project = (joint: Joint) => { x: number; y: number };

// Frozen ride trajectory in board-local space; animated riding needs position history.
export function boardTrailPoint(travel: number, spread = 0, reach = 1): Joint {
  const width = 1 + Math.sin(Math.PI * travel) * 8;
  return [-17 - travel * 108 * reach, travel * travel * 4 + spread * width,
    travel * travel * 62 * reach - Math.abs(spread) * width * 0.4];
}

export function drawBoardTrail(ctx: CanvasRenderingContext2D, project: Project, standing: number) {
  const reach = 0.25 + standing * 0.75;
  ctx.save();
  const alpha = ctx.globalAlpha;
  for (let step = 159; step >= 0; step--) {
    const travel = step / 160;
    const fade = (1 - travel) ** 0.65;
    for (let ribbon = -3; ribbon <= 3; ribbon++) {
      const n = noise(Math.floor(step / 2) * 37 + ribbon * 113 + 67);
      if ((Math.abs(ribbon) > 1 && n > fade * 0.75) || (travel > 0.65 && n > fade)) continue;
      const point = project(boardTrailPoint(travel, ribbon / 3, reach));
      ctx.globalAlpha = alpha * fade * (Math.abs(ribbon) > 1 ? 0.65 : 0.95);
      ctx.fillStyle = ribbon === 0 ? '#e9ffe7' : Math.abs(ribbon) === 1 ? '#d7f7dd' : '#a2e8cf';
      ctx.fillRect(Math.floor(point.x / 2) * 2, Math.floor(point.y / 2) * 2, n > 0.65 ? 4 : 2, 2);
    }
  }
  ctx.restore();
}
