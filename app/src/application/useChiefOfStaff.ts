import { useMemo } from 'react';
import { useProfilesStore } from '../store/profiles';
import { useDaemonStore } from '../store/daemonSessions';

export function useChiefOfStaff() {
  const crew = useDaemonStore((state) => state.crew);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const hasChiefOfStaff = useMemo(
    () => crew.some((member) => member.chief && member.profile_id === selectedProfileId),
    [crew, selectedProfileId],
  );
  return { hasChiefOfStaff };
}
