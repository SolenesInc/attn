import { createBarrelStudy } from '../src/components/SurfBreak/barrelStudies';
import { drawOcean } from '../src/components/SurfBreak/paintOcean';
import type { BarrelTreatment } from '../src/components/SurfBreak/barrelGeometry';

for (const treatment of ['wall', 'hollow', 'curtain'] as const satisfies readonly BarrelTreatment[]) {
  const canvas = document.querySelector<HTMLCanvasElement>(`canvas[data-treatment="${treatment}"]`);
  const status = document.querySelector<HTMLElement>(`[data-status="${treatment}"]`);
  if (!canvas || !status) throw new Error(`Missing ${treatment} study elements`);
  const context = canvas.getContext('2d');
  if (!context) { status.textContent = 'Canvas is unavailable in this browser.'; continue; }
  const { ocean, artwork } = createBarrelStudy(treatment);
  drawOcean(context, ocean, true, artwork);
  canvas.dataset.rendered = 'true';
  status.textContent = 'Canvas drawing, frozen frame';
}
