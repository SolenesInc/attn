import { ChiefOfStaffTransferPrompt } from '../components/ChiefOfStaffTransferPrompt';
import { LocationPicker } from '../components/LocationPicker';
import { SessionContextCapPrompt } from '../components/SessionContextCapPrompt';
import { SessionCreationProgress } from '../components/SessionCreationProgress';
import { UndoToast } from '../components/UndoToast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppShell,
  useChiefOfStaffContext,
  useSessionLaunchContext,
} from './AppContexts';

export function AppSessionPrompts() {
  const {
    locationPickerOpen,
    locationPickerPurpose,
    closeLocationPicker,
    handleLocationSelect,
    handleCreateWorktreeSession,
    sessionCreationJob,
    setSessionCreationJob,
  } = useSessionLaunchContext();
  const {
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    getRepoInfo,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendSetSessionContextWindowCap,
  } = useDaemonApi();
  const { agentAvailability, contextCapPromptSession, setContextCapPromptSession } = useAppShell();
  const { showError } = useAppErrorsContext();
  const { settings, daemonEndpoints } = useAppInputs();
  const {
    hasChiefOfStaff,
    chiefTransferTarget,
    chiefTransferSaving,
    handleConfirmChiefTransfer,
    setChiefTransferTarget,
  } = useChiefOfStaffContext();
  return (
    <>
      <LocationPicker
        isOpen={locationPickerOpen}
        purpose={locationPickerPurpose}
        onClose={closeLocationPicker}
        onSelect={handleLocationSelect}
        onGetRecentLocations={sendGetRecentLocations}
        onBrowseDirectory={sendBrowseDirectory}
        onInspectPath={sendInspectPath}
        onGetRepoInfo={getRepoInfo}
        onCreateWorktree={sendCreateWorktree}
        onCreateWorktreeSession={handleCreateWorktreeSession}
        onDeleteWorktree={sendDeleteWorktree}
        onError={showError}
        projectsDirectory={settings.projects_directory}
        agentAvailability={agentAvailability}
        endpoints={daemonEndpoints}
        chiefExists={hasChiefOfStaff}
      />
      <UndoToast />
      <SessionCreationProgress
        isVisible={sessionCreationJob !== null}
        label={sessionCreationJob?.label || ''}
        path={sessionCreationJob?.path || ''}
        phase={sessionCreationJob?.phase || 'starting_session'}
        error={sessionCreationJob?.error}
        onDismiss={() => setSessionCreationJob(null)}
      />
      <ChiefOfStaffTransferPrompt
        isVisible={chiefTransferTarget !== null}
        currentLabel={chiefTransferTarget?.currentLabel ?? ''}
        targetLabel={chiefTransferTarget?.targetLabel ?? ''}
        isSaving={chiefTransferSaving}
        onConfirm={() => void handleConfirmChiefTransfer()}
        onCancel={() => {
          if (!chiefTransferSaving) {
            setChiefTransferTarget(null);
          }
        }}
      />
      {contextCapPromptSession && (
        <SessionContextCapPrompt
          sessionLabel={contextCapPromptSession.label}
          currentCap={contextCapPromptSession.currentCap}
          onSubmit={(cap) => sendSetSessionContextWindowCap(contextCapPromptSession.id, cap)}
          onClose={() => setContextCapPromptSession(null)}
        />
      )}
    </>
  );
}
