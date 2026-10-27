import { DesktopClosePrompt } from '../components/DesktopClosePrompt';
import { NewSessionLauncher } from '../components/NewSessionLauncher';
import { LocationPicker } from '../components/LocationPicker';
import { SessionContextCapPrompt } from '../components/SessionContextCapPrompt';
import { SessionCreationProgress } from '../components/SessionCreationProgress';
import { SharedCodexPrompts } from '../components/SharedCodexPrompts';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppShell,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';

export function AppSessionPrompts() {
  const { desktopClosePrompt, confirmCloseDesktop, cancelCloseDesktop } =
    useSessionLifecycleContext();
  const {
    locationPickerOpen,
    locationPickerInitialAgent,
    startLocation,
    startWorktreeSession,
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
  const experimental = settings.new_session_launcher_enabled === 'true';
  const pickerProps = {
    initialAgent: locationPickerInitialAgent,
    isOpen: locationPickerOpen,
    onClose: closeLocationPicker,
    onGetRecentLocations: sendGetRecentLocations,
    onBrowseDirectory: sendBrowseDirectory,
    onInspectPath: sendInspectPath,
    onGetRepoInfo: getRepoInfo,
    onCreateWorktree: sendCreateWorktree,
    onDeleteWorktree: sendDeleteWorktree,
    onError: showError,
    projectsDirectory: settings.projects_directory,
    agentAvailability,
  };
  return (
    <>
      {desktopClosePrompt && (
        <DesktopClosePrompt
          label={desktopClosePrompt.label}
          onConfirm={confirmCloseDesktop}
          onCancel={cancelCloseDesktop}
        />
      )}
      <SharedCodexPrompts />
      {experimental ? (
        <NewSessionLauncher
          {...pickerProps}
          onSelect={startLocation}
          onCreateWorktreeSession={(repo, branch, from, agent, yolo, auto, onCreated) =>
            startWorktreeSession(repo, branch, from, agent, yolo, auto, undefined, onCreated)
          }
        />
      ) : (
        <LocationPicker
          {...pickerProps}
          onSelect={launchLocation}
          onCreateWorktreeSession={handleCreateWorktreeSession}
        />
      )}
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
