import { useEffect, useRef } from 'react';
import { AttentionDrawer } from '../components/AttentionDrawer';
import { AsksDrawer } from '../components/Asks/AsksDrawer';
import { AutomationsPanel } from '../components/AutomationsPanel';
import { RightDock } from '../components/RightDock';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  useAppGardenActionsContext,
  useAppInputs,
  useAppPanelsContext,
  useAppSessionsContext,
  useAttentionQueueContext,
  useNavigationContext,
} from './AppContexts';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useSessionStore } from '../store/sessions';

function useTerminalFocusWhenClosed(open: boolean) {
  const wasOpen = useRef(open);
  useEffect(() => {
    const closed = wasOpen.current && !open;
    wasOpen.current = open;
    if (!closed) return;
    const frame = window.requestAnimationFrame(() => {
      const stranded = document.activeElement;
      if (stranded instanceof HTMLElement && stranded.closest('[inert]')) stranded.blur();
      if (document.activeElement === document.body) useSessionStore.getState().requestTerminalFocus();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [open]);
}

export function AppDock() {
  const {
    dockPanelStack,
    closeDockPanel,
    attentionPanelOpen,
    asksPanelOpen,
    automationsPanelOpen,
    gardenPanelOpen,
    gardenHoldsWindow,
    gardenSlotRef,
    closeGarden,
  } = useAppPanelsContext();
  const { waitingLocalSessions } = useAttentionQueueContext();
  const { prs } = useAppInputs();
  const { handleSelectSession } = useNavigationContext();
  const { handleRevealSeedInGarden } = useAppGardenActionsContext();
  const { profileSessions } = useAppSessionsContext();
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
    asks: asksPanelOpen,
    automations: automationsPanelOpen,
    garden: gardenPanelOpen,
  };
  const topPanel = dockPanelStack.slice().reverse().find((id) => openPanels[id]);
  useTerminalFocusWhenClosed(asksPanelOpen);
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
            id: 'asks',
            isOpen: openPanels.asks,
            width: 'clamp(420px, 46vw, 720px)',
            className: 'dock-panel dock-panel--asks',
            children: dockPanelStack.includes('asks') && (
              <AsksDrawer
                sessions={profileSessions}
                onTop={topPanel === 'asks' && !gardenHoldsWindow}
                onClose={() => closeDockPanel('asks')}
                onOpenAgent={(sessionId) => {
                  closeDockPanel('asks');
                  handleSelectSession(sessionId);
                }}
                onOpenSeed={handleRevealSeedInGarden}
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
