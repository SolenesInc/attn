import { Ocean } from './ocean';
import type { OceanArtwork } from './paintOcean';
import type { BarrelTreatment } from './barrelGeometry';

export function createBarrelStudy(treatment: BarrelTreatment): { ocean: Ocean; artwork: OceanArtwork } {
  const ocean = new Ocean({ beach: 'point' });
  ocean.camera.x = 900;
  ocean.camera.y = -28;
  ocean.x = 1340;
  ocean.y = 294;
  ocean.z = 0;
  ocean.heading = treatment === 'curtain' ? 0 : Math.PI;
  ocean.posture = 'standing';
  ocean.standingBlend = 1;
  // Art poses are fixed independently of wave physics while the rendering is being reviewed.
  return { ocean, artwork: { surferScale: 1.8, detailedSurfer: true, barrels: [{
    treatment, left: 1070, floor: 294, width: 660, height: 105,
    roundness: treatment === 'hollow' ? 1 : 0,
    curtain: treatment === 'curtain' ? 1 : 0,
    lipFall: treatment === 'curtain' ? 1 : 0.78,
    curlSide: 'left',
    peelDirection: treatment === 'curtain' ? 'right' : 'left',
    seed: 23,
  }] } };
}
