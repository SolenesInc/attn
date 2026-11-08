import { CalloutOverlay } from '../callouts/CalloutOverlay';
import { openUrl } from '@tauri-apps/plugin-opener';
import { BannerStack } from '../components/BannerStack';
import { ChordLeaderHud } from '../components/ChordLeaderHud';
import { DelegationChainProvider } from '../components/DelegationChain';
import { DiagnosticReportPrompt } from '../components/DiagnosticReportPrompt';
import { SenderPairingPrompt } from '../components/SenderPairingPrompt';
import { TitleBar } from '../components/TitleBar';
import { OpenPRLauncherProgress } from '../components/OpenPRLauncherProgress';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { DaemonProvider } from '../contexts/DaemonContext';
import { GitHubPollingProvider } from '../contexts/GitHubPollingContext';
import { NotebookSurfaceProvider } from '../contexts/NotebookSurfaceContext';
import { useActiveLeaf } from '../hooks/useDesktopSelectionBridge';
import {
  useAppDiagnosticsContext,
  useAppInputs,
  useAppNotebookSurfaceContext,
  useAppPanelsContext,
  useAppSessionsContext,
  useAppShell,
  useAttentionQueueContext,
  useNavigationContext,
  usePRLauncherContext,
} from './AppContexts';
import { AppDashboard } from './AppDashboard';
import { AppDock } from './AppDock';
import { AppCrewPanel } from './AppCrewPanel';
import { AppLibrarySurfaces } from './AppLibrarySurfaces';
import { AppNavigationMenus } from './AppNavigationMenus';
import { AppPreferences } from './AppPreferences';
import { AppSessionPrompts } from './AppSessionPrompts';
import { AppSidebar, AppSidebarProvider } from './AppSidebar';
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
  } = useAppInputs();
  const { notebookSurfaceContextValue } = useAppNotebookSurfaceContext();
  const { snoozeMenu } = useAttentionQueueContext();
  const { windowCovered, appShellRef, agentFocused } = useAppShell();
  const { delegationChainRef } = useAppPanelsContext();
  const { requestTerminalFocus, handleSelectSession, view } = useNavigationContext();
  const { delegationSessions } = useAppSessionsContext();
  const leaf = useActiveLeaf();
  const { openPRLauncherJob } = usePRLauncherContext();
  const {
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
            navigationKey={`${view}:${leaf?.profileId ?? ''}:${leaf?.desktopId ?? ''}:${leaf?.leafId ?? ''}`}
            blocked={windowCovered || Boolean(snoozeMenu)}
          >
            <div
              className={`app${agentFocused ? ' is-agent-focused' : ''}`}
              ref={appShellRef}
              tabIndex={-1}
              style={{ outline: 'none' }}
              onPointerDownCapture={handleAppPointerDownCapture}
            >
              {openPRLauncherJob && (
                <OpenPRLauncherProgress
                  repo={openPRLauncherJob.pr.repo}
                  number={openPRLauncherJob.pr.number}
                  title={openPRLauncherJob.pr.title}
                  step={openPRLauncherJob.progress.step}
                />
              )}
              <AppSidebarProvider>
                <div className="app-main">
                  <TitleBar />
                  <BannerStack
                    connectionError={connectionError}
                    warnings={warnings}
                    onOpenWarningUrl={(url) => {
                      openUrl(url).catch((err) => {
                        console.error('[App] Failed to open warning link:', err);
                      });
                    }}
                    onClearWarnings={clearWarnings}
                  />
                  <div className="app-frame">
                    <AppSidebar />
                    <div className="view-stack">
                      {/* Always rendered, to keep its scroll position. */}
                      <AppDashboard />
                      {/* Always rendered, to keep terminals alive. */}
                      <div className={`view-container ${view === 'session' ? 'visible' : 'hidden'}`}>
                        <div className="terminal-pane">
                          <AppDesktops />
                        </div>
                      </div>
                      <AppDock />
                      <AppCrewPanel />
                    </div>
                  </div>
                </div>
              </AppSidebarProvider>

              {/* Yield library focus traps before session prompts take focus in layout effects. */}
              <AppLibrarySurfaces />
              <AppSessionPrompts />
              <ChordLeaderHud />
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
              <SenderPairingPrompt />
              <CalloutOverlay />
            </div>
          </DelegationChainProvider>
        </NotebookSurfaceProvider>
      </GitHubPollingProvider>
    </DaemonProvider>
  );
}
