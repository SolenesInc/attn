import { AttentionDrawer } from '../components/AttentionDrawer';
import { AutomationsPanel } from '../components/AutomationsPanel';
import { RightDock } from '../components/RightDock';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  useAppInputs,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
} from './AppContexts';
import { useEscapeStack } from '../hooks/useEscapeStack';

export function AppDock() {
  const {
    dockPanelStack,
    closeDockPanel,
    attentionPanelOpen,
    automationsPanelOpen,
    gardenPanelOpen,
    gardenHoldsWindow,
    gardenSlotRef,
    closeGarden,
  } = useAppPanelsContext();
  const { waitingLocalSessions } = useAttentionQueueContext();
  const { prs } = useAppInputs();
  const { handleSelectSession } = useNavigationContext();
  const {
    listAutomationDefinitions,
    listAutomationRuns,
    setAutomationEnabled,
    runAutomationNow,
    getAutomationDefinition,
    applyAutomationDefinition,
    deleteAutomationDefinition,
  } = useDaemonApi();
  const openPanels = {
    attention: attentionPanelOpen,
    automations: automationsPanelOpen,
    garden: gardenPanelOpen,
  };
  const topPanel = dockPanelStack.slice().reverse().find((id) => openPanels[id]);
  useEscapeStack(() => {
    if (gardenHoldsWindow || topPanel === 'garden') closeGarden();
    else if (topPanel) closeDockPanel(topPanel);
  }, gardenHoldsWindow || !!topPanel);
  return (
    <div className="app-dock">
      <RightDock
        panelOrder={dockPanelStack}
        panels={[
          {
            id: 'attention',
            isOpen: openPanels.attention,
            width: 'clamp(360px, 48vw, 600px)',
            className: 'dock-panel dock-panel--attention attention-drawer',
            children: (
              <AttentionDrawer
                onClose={() => closeDockPanel('attention')}
                waitingSessions={waitingLocalSessions}
                prs={prs}
                onSelectSession={handleSelectSession}
              />
            ),
          },
          {
            id: 'automations',
            isOpen: openPanels.automations,
            width: 'clamp(420px, 42vw, 640px)',
            className: 'dock-panel dock-panel--automations',
            children: (
              <AutomationsPanel
                isOpen={automationsPanelOpen}
                onClose={() => closeDockPanel('automations')}
                fetchDefinitions={listAutomationDefinitions}
                fetchRuns={listAutomationRuns}
                setEnabled={setAutomationEnabled}
                runNow={runAutomationNow}
                getDefinition={getAutomationDefinition}
                applyDefinition={applyAutomationDefinition}
                deleteDefinition={deleteAutomationDefinition}
                onSelectSession={handleSelectSession}
              />
            ),
          },
          {
            id: 'garden',
            width: 'clamp(380px, 34vw, 560px)',
            isOpen: gardenPanelOpen && !gardenHoldsWindow,
            detached: gardenSlotRef,
            children: null,
          },
        ]}
      />
    </div>
  );
}
