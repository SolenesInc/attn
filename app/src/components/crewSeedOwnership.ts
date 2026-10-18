import type { Seed } from '../hooks/useDaemonSocket';
import type { CrewMember } from '../types/generated';

export type CrewSeedFilter = 'tending' | 'planted';

export function seedsTendedByMember(seeds: Seed[], member: CrewMember): Seed[] {
  return seeds.filter((seed) => seed.claimed && seed.tender?.ref === `member:${member.key}`);
}

export function seedsPlantedByMember(seeds: Seed[], memberId: string): Seed[] {
  return seeds.filter((seed) => seed.planter.ref === `member:${memberId}`);
}
