import type { Seed } from '../hooks/useDaemonSocket';
import type { CrewMember } from '../types/generated';
import { tendedSeeds } from './paneSeedDisplay';

export type CrewSeedFilter = 'tending' | 'planted';

export function seedsTendedByMember(seeds: Seed[], member: CrewMember): Seed[] {
  return tendedSeeds(seeds, member.binding_session ?? '', member.id);
}

export function seedsPlantedByMember(seeds: Seed[], memberId: string): Seed[] {
  return seeds.filter((seed) => seed.planter_member === memberId);
}
