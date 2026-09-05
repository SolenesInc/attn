import type { Ocean } from './ocean';
import type { WaterRect } from './paintWave';
import { noise, OCEAN_HEIGHT, OCEAN_WIDTH, SEA_LEVEL } from './water';

export function drawBarrelSetting(rect: WaterRect, ocean: Ocean) {
  const horizon = 246;
  const surface = ocean.project(ocean.camera.x, SEA_LEVEL + 24).y;
  for (let band = 0; band < 24; band++) {
    const f = band / 23;
    rect(0, band * horizon / 24, OCEAN_WIDTH, horizon / 24 + 1,
      `rgb(${Math.round(107 + f * 14)}, ${Math.round(173 + f * 14)}, ${Math.round(233 + f * 9)})`);
  }
  const islandX = 55 - ocean.camera.x * 0.06;
  for (let x = 0; x < 110; x += 3) {
    const peak = Math.max(0, (1 - x / 110) * 24 + Math.sin(x * 0.12) * 4);
    const ridge = Math.round((horizon - peak) / 3) * 3;
    rect(islandX + x, ridge, 3, horizon - ridge, '#294d70');
    if (noise(x + 81) > 0.65) rect(islandX + x, ridge + 6, 3, 3, '#375b7b');
  }
  for (let y = horizon; y < surface; y += 3) {
    const depth = (y - horizon) / (surface - horizon);
    rect(0, y, OCEAN_WIDTH, 3,
      `rgb(${Math.round(38 - depth * 15)}, ${Math.round(171 - depth * 27)}, ${Math.round(185 - depth * 22)})`);
  }
  for (let i = 0; i < 460; i++) {
    const y = horizon + noise(i + 30) * (surface - horizon);
    const x = noise(i + 17) * OCEAN_WIDTH;
    rect(x, y, 3 + noise(i + 89) * 17, 1, i % 4 ? '#249eaf' : '#32aebe');
  }
  rect(0, surface, OCEAN_WIDTH, OCEAN_HEIGHT - surface, '#167f91');
  for (let y = surface; y < OCEAN_HEIGHT; y += 3) {
    const depth = (y - surface) / (OCEAN_HEIGHT - surface);
    rect(0, y, OCEAN_WIDTH, 3,
      `rgb(${Math.round(21 - depth * 1)}, ${Math.round(128 - depth * 17)}, ${Math.round(146 - depth * 12)})`);
  }
  rect(0, surface, OCEAN_WIDTH, 2, '#4bbaaf');
  for (let i = 0; i < 64; i++) {
    rect(noise(i + 97) * OCEAN_WIDTH, surface + noise(i + 35) * 61,
      2, 1 + Number(i % 3 === 0), i % 4 ? '#198b99' : '#28a4ad');
  }
}
