import { CrewPanel } from '../components/CrewPanel';
import { useDaemonStore } from '../store/daemonSessions';
import { useAppGardenActionsContext, useAppInputs, useCrewPanelContext } from './AppContexts';

export function AppCrewPanel() {
  const { daemonSessions } = useAppInputs();
  const crew = useDaemonStore((state) => state.crew);
  const seeds = useDaemonStore((state) => state.seeds);
  const seedsTotal = useDaemonStore((state) => state.seedsTotal);
  const { crewPanel, handleCloseCrew } = useCrewPanelContext();
  const { handleOpenSeedFromCrew } = useAppGardenActionsContext();
  return (
    <CrewPanel
      visit={crewPanel.visit}
      isOpen={crewPanel.open}
      initialMember={crewPanel.member}
      members={crew}
      sessions={daemonSessions}
      seeds={seeds}
      seedsTotal={seedsTotal}
      onClose={handleCloseCrew}
      onOpenSeed={handleOpenSeedFromCrew}
    />
  );
}
