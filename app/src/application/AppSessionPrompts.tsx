import { ChiefOfStaffTransferPrompt } from '../components/ChiefOfStaffTransferPrompt';
import { CloseSessionPrompt } from '../components/CloseSessionPrompt';
import { LocationPicker } from '../components/LocationPicker';
import { SessionContextCapPrompt } from '../components/SessionContextCapPrompt';
import { SessionCreationProgress } from '../components/SessionCreationProgress';
import { UndoToast } from '../components/UndoToast';
import { AppViewParamsPrompt } from '../components/appViews/AppViewParamsPrompt';
import { useAppContext } from './AppContext';

export function AppSessionPrompts() {
  const {
    locationPickerOpen,
    locationPickerPurpose,
    closeLocationPicker,
    handleLocationSelect,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    getRepoInfo,
    sendCreateWorktree,
    handleCreateWorktreeSession,
    sendDeleteWorktree,
    showError,
    settings,
    agentAvailability,
    daemonEndpoints,
    hasChiefOfStaff,
    sessionCreationJob,
    setSessionCreationJob,
    pendingSessionClose,
    handleConfirmSessionClose,
    handleCancelSessionClose,
    chiefTransferTarget,
    chiefTransferSaving,
    handleConfirmChiefTransfer,
    setChiefTransferTarget,
    appViewParamsPrompt,
    dockAppViewTile,
    setAppViewParamsPrompt,
    contextCapPromptSession,
    sendSetSessionContextWindowCap,
    setContextCapPromptSession,
  } = useAppContext();
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
      <CloseSessionPrompt
        isVisible={pendingSessionClose !== null}
        sessionLabel={pendingSessionClose?.label || ''}
        splitCount={pendingSessionClose?.splitCount || 0}
        onConfirm={handleConfirmSessionClose}
        onCancel={handleCancelSessionClose}
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
      {appViewParamsPrompt && (
        <AppViewParamsPrompt
          viewTitle={appViewParamsPrompt.viewTitle}
          label={appViewParamsPrompt.label}
          placeholder={appViewParamsPrompt.placeholder}
          onSubmit={(params) =>
            dockAppViewTile(appViewParamsPrompt.app, appViewParamsPrompt.view, params)
          }
          onClose={() => setAppViewParamsPrompt(null)}
        />
      )}
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
