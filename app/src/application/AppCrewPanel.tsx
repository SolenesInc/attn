import { useMemo } from 'react';
import { CrewPanel } from '../components/CrewPanel';
import { useProfilesStore } from '../store/profiles';
import { useDaemonStore } from '../store/daemonSessions';
import { useAppGardenActionsContext, useAppInputs, useCrewPanelContext } from './AppContexts';

export function AppCrewPanel() {
  const { daemonSessions, settings } = useAppInputs();
  const allCrew = useDaemonStore((state) => state.crew);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const crew = useMemo(
    () => allCrew.filter((member) => member.profile_id === selectedProfileId),
    [allCrew, selectedProfileId],
  );
  const seeds = useDaemonStore((state) => state.seeds);
  const seedsTotal = useDaemonStore((state) => state.seedsTotal);
  const { crewPanel, handleCloseCrew } = useCrewPanelContext();
  const { handleOpenSeedFromCrew, handleWakeCrewMember, handleSleepCrewMember } = useAppGardenActionsContext();
  return (
    <CrewPanel
      visit={crewPanel.visit}
      isOpen={crewPanel.open}
      initialMember={crewPanel.member}
      initialTab={crewPanel.tab}
      members={crew}
      canRename={settings.crew_v2_enabled === 'true'}
      sessions={daemonSessions}
      seeds={seeds}
      seedsTotal={seedsTotal}
      onClose={handleCloseCrew}
      onWakeMember={handleWakeCrewMember}
      onSleepMember={handleSleepCrewMember}
      onOpenSeed={handleOpenSeedFromCrew}
    />
  );
}
