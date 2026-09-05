export type BeachId = 'cove' | 'sandbar' | 'point' | 'reef' | 'nazare';
export type SurfConditions = { size: 'small' | 'usual' | 'large'; rhythm: 'quiet' | 'steady' | 'frequent' };
export const defaultConditions = (): SurfConditions => ({ size: 'usual', rhythm: 'steady' });
export type Beach = {
  id: BeachId; name: string; description: string;
  slope: number; offshore: number; amplitude: number; period: number;
  setSize: number; lull: number; peel: number; hollow: number; breakRun: number;
  turnLoss: number; wipeoutHeight: number; sky: string; sand: string;
};

export const beaches: Record<BeachId, Beach> = {
  cove: { id: 'cove', name: 'Sheltered cove', description: 'Soft spilling waves. Plenty of time between sets.',
    slope: 0.085, offshore: 1500, amplitude: 16, period: 6, setSize: 2, lull: 30, peel: 0.2,
    hollow: 0.18, breakRun: 380, turnLoss: 0.02, wipeoutHeight: Infinity, sky: '#99c8f4', sand: '#d9bb85' },
  sandbar: { id: 'sandbar', name: 'Sandbar beach', description: 'Changing peaks and a mix of soft and hollow waves.',
    slope: 0.09, offshore: 1900, amplitude: 40, period: 7, setSize: 3, lull: 18, peel: 0.45,
    hollow: 0.75, breakRun: 340, turnLoss: 0.06, wipeoutHeight: 90, sky: '#95c3f2', sand: '#d9bb85' },
  point: { id: 'point', name: 'Long point', description: 'Long peeling faces. Room to carve and walk the board.',
    slope: 0.055, offshore: 2700, amplitude: 66, period: 11, setSize: 3, lull: 26, peel: 1.4,
    hollow: 0.9, breakRun: 1200, turnLoss: 0.04, wipeoutHeight: 100, sky: '#91bff0', sand: '#c4b083' },
  reef: { id: 'reef', name: 'Reef break', description: 'Steep takeoffs and hollow tubes over a shallow shelf.',
    slope: 0.07, offshore: 2200, amplitude: 58, period: 8, setSize: 3, lull: 22, peel: 0.65,
    hollow: 1, breakRun: 260, turnLoss: 0.16, wipeoutHeight: 65, sky: '#8dbde5', sand: '#9e9980' },
  nazare: { id: 'nazare', name: 'Nazaré', description: 'Giant waves. Takeoff timing and your line matter.',
    slope: 0.115, offshore: 3400, amplitude: 115, period: 12, setSize: 2, lull: 35, peel: 0.85,
    hollow: 1, breakRun: 1250, turnLoss: 0.38, wipeoutHeight: 75, sky: '#91adbf', sand: '#bda783' },
};

const smooth = (v: number) => { const n = Math.max(0, Math.min(1, v)); return n * n * (3 - 2 * n); };
export function bottomDepth(beach: Beach, x: number, z = 0) {
  const coast = x - Math.sin(z * 0.025) * 18;
  let depth = coast * beach.slope;
  if (coast <= 0) return Math.max(-45, depth);
  if (beach.id === 'sandbar') depth -= 25 * Math.exp(-(((coast - 610 - z * 0.7) / 170) ** 2));
  if (beach.id === 'point') depth += z * 0.22 * smooth(coast / 500);
  if (beach.id === 'reef') depth += 85 * smooth((coast - 740 - z * 0.55) / 230);
  if (beach.id === 'nazare') depth += 190 * smooth((coast - 1550) / 650) * (0.65 + z * 0.0035);
  return Math.max(0, depth + Math.sin(coast * 0.018) * Math.min(3, coast * 0.01));
}

export function swellAmplitude(beach: Beach, conditions: SurfConditions) {
  return beach.amplitude * ({ small: 0.7, usual: 1, large: 1.25 }[conditions.size]);
}

export function setLull(beach: Beach, conditions: SurfConditions) {
  return beach.lull * ({ quiet: 1.8, steady: 1, frequent: 0.45 }[conditions.rhythm]);
}
