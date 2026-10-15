import { DesktopClosePrompt } from '../components/DesktopClosePrompt';
import { LocationPicker } from '../components/LocationPicker';
import { SessionContextCapPrompt } from '../components/SessionContextCapPrompt';
import { SessionCreationProgress } from '../components/SessionCreationProgress';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppShell,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';

export function AppSessionPrompts() {
  const { desktopClosePrompt, confirmCloseDesktop, cancelCloseDesktop } = useSessionLifecycleContext();
  const {
    locationPickerOpen,
    closeLocationPicker,
    launchLocation,
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
  const { settings } = useAppInputs();
  return (
    <>
      {desktopClosePrompt && <DesktopClosePrompt label={desktopClosePrompt.label} onConfirm={confirmCloseDesktop} onCancel={cancelCloseDesktop} />}
      <LocationPicker
        isOpen={locationPickerOpen}
        onClose={closeLocationPicker}
        onSelect={launchLocation}
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
      />
      <SessionCreationProgress
        isVisible={sessionCreationJob !== null}
        label={sessionCreationJob?.label || ''}
        path={sessionCreationJob?.path || ''}
        phase={sessionCreationJob?.phase || 'starting_session'}
        error={sessionCreationJob?.error}
        onDismiss={() => setSessionCreationJob(null)}
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
