import { CrewPanel } from '../components/CrewPanel';
import { useDaemonStore } from '../store/daemonSessions';
import { useAppGardenActionsContext, useAppInputs, useChiefContext, useCrewPanelContext } from './AppContexts';

export function AppCrewPanel() {
  const chief = useChiefContext();
  const { daemonSessions } = useAppInputs();
  const crew = useDaemonStore((state) => state.crew);
  const seeds = useDaemonStore((state) => state.seeds);
  const seedsTotal = useDaemonStore((state) => state.seedsTotal);
  const { crewPanel, handleCloseCrew } = useCrewPanelContext();
  const { handleOpenSeedFromCrew, handleWakeCrewMember } = useAppGardenActionsContext();
  return (
    <CrewPanel
      visit={crewPanel.visit}
      isOpen={crewPanel.open}
      initialMember={crewPanel.member}
      initialTab={crewPanel.tab}
      members={[...crew].sort((a, b) => Number(b.chief) - Number(a.chief))}
      sessions={daemonSessions}
      seeds={seeds}
      seedsTotal={seedsTotal}
      onClose={handleCloseCrew}
      onWakeMember={member => member === chief.view?.key ? chief.actions.wake() : handleWakeCrewMember(member)}
      onOpenSeed={handleOpenSeedFromCrew}
    />
  );
}
