import { AttentionDrawer } from '../components/AttentionDrawer';
import { AutomationsPanel } from '../components/AutomationsPanel';
import { RightDock } from '../components/RightDock';
import { WorkflowRunView } from '../components/WorkflowRunView';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useSessionStore } from '../store/sessions';
import {
  useAppInputs,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
  useWorkflowPanelContext,
} from './AppContexts';
import { toneForDockPanel } from './appSupport';

export function AppDock() {
  const {
    dockPanelStack,
    workflowRunPanelOpen,
    closeDockPanel,
    attentionPanelOpen,
    automationsPanelOpen,
    gardenPanelOpen,
    gardenHoldsWindow,
    gardenSlotRef,
  } = useAppPanelsContext();
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const { activeWorkflowRun } = useWorkflowPanelContext();
  const { waitingLocalSessions } = useAttentionQueueContext();
  const { prs } = useAppInputs();
  const { handleSelectSession, selectAgentPane } = useNavigationContext();
  const {
    listAutomationDefinitions,
    listAutomationRuns,
    setAutomationEnabled,
    runAutomationNow,
    getAutomationDefinition,
    applyAutomationDefinition,
    deleteAutomationDefinition,
  } = useDaemonApi();
  return (
    <>
      <RightDock
        panelOrder={dockPanelStack}
        panels={[
          {
            id: 'workflowRun',
            isOpen: workflowRunPanelOpen && Boolean(activeSessionId),
            width: 'clamp(420px, 50vw, 680px)',
            tone: activeWorkflowRun ? toneForDockPanel(activeWorkflowRun.status) : 'default',
            className: 'dock-panel dock-panel--workflow-run',
            children: activeSessionId ? (
              <WorkflowRunView
                run={activeWorkflowRun}
                onClose={() => closeDockPanel('workflowRun')}
              />
            ) : null,
          },
          {
            id: 'attention',
            isOpen: attentionPanelOpen,
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
            isOpen: automationsPanelOpen,
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
                onFocusPane={(sessionId, paneId) => selectAgentPane(sessionId, paneId)}
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
    </>
  );
}
