import { DesktopClosePrompt } from '../components/DesktopClosePrompt';
import { NewSessionLauncher } from '../components/NewSessionLauncher';
import { LocationPicker } from '../components/LocationPicker';
import { SessionContextCapPrompt } from '../components/SessionContextCapPrompt';
import { SessionCreationProgress } from '../components/SessionCreationProgress';
import { SharedCodexPrompts } from '../components/SharedCodexPrompts';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import {
  useAppErrorsContext,
  useAppInputs,
  useAppShell,
  useSessionLaunchContext,
  useSessionLifecycleContext,
} from './AppContexts';
import { BoolSetting, isOn } from '../utils/settings';

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
  const experimental = isOn(settings, BoolSetting.newSessionLauncherEnabled);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
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
          desktopId={currentDesktopId ?? undefined}
          onSelect={startLocation}
          onCreateWorktreeSession={startWorktreeSession}
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
