import { openUrl } from '@tauri-apps/plugin-opener';
import { BannerStack } from '../components/BannerStack';
import { ChordLeaderHud } from '../components/ChordLeaderHud';
import { DelegationChainProvider } from '../components/DelegationChain';
import { DiagnosticReportPrompt } from '../components/DiagnosticReportPrompt';
import { ErrorToast } from '../components/ErrorToast';
import { OpenPRLauncherProgress } from '../components/OpenPRLauncherProgress';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { DaemonProvider } from '../contexts/DaemonContext';
import { GitHubPollingProvider } from '../contexts/GitHubPollingContext';
import { NotebookSurfaceProvider } from '../contexts/NotebookSurfaceContext';
import { useSessionStore } from '../store/sessions';
import {
  useAppDiagnosticsContext,
  useAppErrorsContext,
  useAppInputs,
  useAppNotebookSurfaceContext,
  useAppPanelsContext,
  useAppSessionsContext,
  useAppShell,
  useNavigationContext,
  usePRLauncherContext,
  useDesktopTilesContext,
} from './AppContexts';
import { AppDashboard } from './AppDashboard';
import { AppDock } from './AppDock';
import { AppGrid } from './AppGrid';
import { AppCrewPanel } from './AppCrewPanel';
import { AppLibrarySurfaces } from './AppLibrarySurfaces';
import { AppNavigationMenus } from './AppNavigationMenus';
import { AppPreferences } from './AppPreferences';
import { AppSessionPrompts } from './AppSessionPrompts';
import { AppSidebar } from './AppSidebar';
import { AppDesktopNavigation } from './AppDesktopNavigation';
import { AppDesktops } from './AppDesktops';
import { handleAppPointerDownCapture } from './appSupport';
export function AppSurface() {
  const {
    sendPRAction,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendPRVisited,
    connectionError,
    warnings,
    clearWarnings,
  } = useDaemonApi();
  const {
    githubPollingOffReason,
    updateAvailableVersion,
    onOpenLatestRelease,
    onDismissLatestRelease,
  } = useAppInputs();
  const { notebookSurfaceContextValue } = useAppNotebookSurfaceContext();
  const { blockingOverlayOpen, appShellRef } = useAppShell();
  const { errorMessage, errorDurationMs, clearError } = useAppErrorsContext();
  const { delegationChainRef } = useAppPanelsContext();
  const { requestTerminalFocus, handleSelectSession, view } = useNavigationContext();
  const { delegationSessions } = useAppSessionsContext();
  const activeSessionId = useSessionStore((state) => state.activeSessionId);
  const { markdownOpenerOpen } = useDesktopTilesContext();
  const { openPRLauncherJob } = usePRLauncherContext();
  const {
    diagnosticReportSaved,
    diagnosticCapture,
    handleSaveDiagnosticReport,
    setDiagnosticCapture,
  } = useAppDiagnosticsContext();
  return (
    <DaemonProvider
      sendPRAction={sendPRAction}
      sendMutePR={sendMutePR}
      sendMuteRepo={sendMuteRepo}
      sendMuteAuthor={sendMuteAuthor}
      sendPRVisited={sendPRVisited}
    >
      <GitHubPollingProvider offReason={githubPollingOffReason}>
        <NotebookSurfaceProvider value={notebookSurfaceContextValue}>
          <DelegationChainProvider
            onRestoreFocusFallback={requestTerminalFocus}
            ref={delegationChainRef}
            sessions={delegationSessions}
            onSelectSession={handleSelectSession}
            navigationKey={`${view}:${activeSessionId ?? ''}`}
            blocked={blockingOverlayOpen || markdownOpenerOpen}
          >
            <div
              className="app"
              ref={appShellRef}
              tabIndex={-1}
              style={{ outline: 'none' }}
              onPointerDownCapture={handleAppPointerDownCapture}
            >
              <BannerStack
                connectionError={connectionError}
                warnings={warnings}
                updateAvailableVersion={updateAvailableVersion}
                onOpenWarningUrl={(url) => {
                  openUrl(url).catch((err) => {
                    console.error('[App] Failed to open warning link:', err);
                  });
                }}
                onClearWarnings={clearWarnings}
                onOpenLatestRelease={onOpenLatestRelease}
                onDismissLatestRelease={onDismissLatestRelease}
              />
              {openPRLauncherJob && (
                <OpenPRLauncherProgress
                  repo={openPRLauncherJob.pr.repo}
                  number={openPRLauncherJob.pr.number}
                  title={openPRLauncherJob.pr.title}
                  step={openPRLauncherJob.progress.step}
                />
              )}
              <div className="app-frame">
                <AppSidebar />
                <div className="view-stack">
                  {/* Always rendered; shown/hidden via z-index. */}
                  <AppDashboard />
                  {/* Always rendered, to keep terminals alive. */}
                  <div className={`view-container ${view === 'session' ? 'visible' : 'hidden'}`}>
                    <div className="terminal-pane">
                      <AppDesktops />
                    </div>
                    <AppDock />
                  </div>
                  <AppCrewPanel />
                </div>
              </div>

              {/* Mounted only while active, so its WebGL context is released on exit. */}
              <AppGrid />
              <AppSessionPrompts />
              <ErrorToast message={errorMessage} durationMs={errorDurationMs} onDone={clearError} />
              {diagnosticReportSaved.saved('saved') && (
                <div className="input-diagnostics-copied" role="status">
                  Diagnostic report saved
                </div>
              )}
              <ChordLeaderHud />
              <AppLibrarySurfaces />
              <AppNavigationMenus />
              <AppDesktopNavigation />
              {diagnosticCapture && (
                <DiagnosticReportPrompt
                  capture={diagnosticCapture.capture}
                  affectedPaneId={diagnosticCapture.affectedPaneId}
                  onCreate={handleSaveDiagnosticReport}
                  onClose={() => setDiagnosticCapture(null)}
                />
              )}
              <AppPreferences />
            </div>
          </DelegationChainProvider>
        </NotebookSurfaceProvider>
      </GitHubPollingProvider>
    </DaemonProvider>
  );
}
