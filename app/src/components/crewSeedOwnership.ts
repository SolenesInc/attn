import type { Seed } from '../hooks/useDaemonSocket';
import type { CrewMember } from '../types/generated';
import { crewRef } from '../utils/crewName';

export type CrewSeedFilter = 'tending' | 'planted';

export function seedsTendedByMember(seeds: Seed[], member: CrewMember): Seed[] {
  const ref = crewRef(member.id);
  const session = member.binding_session ?? '';
  return seeds.filter((seed) => seed.tender?.ref === ref || Boolean(session && seed.tender?.session_id === session));
}

export function seedsPlantedByMember(seeds: Seed[], memberId: string): Seed[] {
  const ref = crewRef(memberId);
  return seeds.filter((seed) => seed.planter.ref === ref);
}
