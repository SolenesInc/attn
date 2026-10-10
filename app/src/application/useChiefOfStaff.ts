import { useMemo } from 'react';
import { useProfilesStore } from '../store/profiles';
import { AppContentProps } from './appSupport';

export function useChiefOfStaff({ daemonSessions }: { daemonSessions: AppContentProps['daemonSessions'] }) {
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const hasChiefOfStaff = useMemo(
    () => daemonSessions.some((session) => session.chief === true && session.profile_id === selectedProfileId),
    [daemonSessions, selectedProfileId],
  );
  return { hasChiefOfStaff };
}
