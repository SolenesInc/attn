import { useState } from 'react';
import { AnnotatedTerminal } from '../TerminalAnnotations/AnnotatedTerminal';
import { TerminalStaleBuildNotice } from '../TerminalStaleBuildNotice';
import { useDesktopContext } from './DesktopContext';
import { paneNotice } from './paneNotice';
import type { DesktopAgentProps } from './desktopTypes';
import { useSessionStore } from '../../store/sessions';

export function DesktopAgentBody({ agentPane, paneSession, paneTitle }: DesktopAgentProps) {
  const [resuming, setResuming] = useState(false);
  const [resumeError, setResumeError] = useState('');
  const {
    desktopId,
    gardenSeeds,
    onOpenSeed,
    annotationApi,
    activeAgentPaneId,
    fontSize,
    resolvedTheme,
    isActiveSession,
    terminalsLive,
    onTerminalPointerActivity,
    onOpenMarkdown,
    staleBuildDismissed,
    setStaleBuildDismissed,
    paneIds,
    activeLeafId,
    runtime,
    terminalRefForPane,
    sessionVisible,
    shortcutsEnabled,
    handleGhosttyTerminalReady,
    handleClosePane,
  } = useDesktopContext();
  const notice = paneNotice(agentPane, paneSession, paneTitle);
  const stopped = paneSession?.terminalExit || paneSession?.state === 'recoverable';
  const resume = async () => {
    setResumeError('');
    setResuming(true);
    try {
      await useSessionStore.getState().reloadSession(agentPane.sessionId, runtime.getPaneSize(agentPane.id) ?? undefined);
    } catch (error) {
      setResumeError(error instanceof Error ? error.message : String(error));
    } finally {
      setResuming(false);
    }
  };

  return (
    <div className="desktop-pane-body">
      {stopped ? (
        <div className="desktop-agent-stopped" role="status">
          <div>
            <strong>Agent stopped.</strong> Resume to continue this conversation.
            {resumeError ? <div role="alert">{resumeError}</div> : null}
          </div>
          <button type="button" disabled={resuming} onClick={() => void resume()}>{resuming ? 'Resuming…' : 'Resume'}</button>
          <button type="button" disabled={resuming} onClick={() => handleClosePane(agentPane.id)}>Close</button>
        </div>
      ) : null}
      {paneSession?.terminalBuildStale && !staleBuildDismissed.has(agentPane.sessionId) ? (
        <TerminalStaleBuildNotice
          onDismiss={() => setStaleBuildDismissed((prev) => new Set(prev).add(agentPane.sessionId))}
        />
      ) : null}
      {notice ? (
        <div className={`desktop-pane-status desktop-pane-status--${notice.tone}`}>
          <span className="desktop-pane-status-spinner" aria-hidden="true" />
          <span>{notice.text}</span>
        </div>
      ) : !terminalsLive ? (
        <div
          className="desktop-pane-virtualized"
          aria-hidden="true"
          data-testid={`pane-virtualized-${agentPane.id}`}
        />
      ) : (
        <AnnotatedTerminal
          ref={terminalRefForPane(agentPane.id)}
          desktopId={desktopId}
          sessionId={agentPane.sessionId}
          annotationApi={annotationApi}
          // At most one pane owns ⌘Enter for the annotation send shortcut.
          paneActive={isActiveSession && sessionVisible && shortcutsEnabled && activeLeafId === agentPane.id}
          fontSize={fontSize}
          resolvedTheme={resolvedTheme}
          cwd={paneSession?.cwd}
          debugName={`agent:${paneTitle}:${paneSession?.agent ?? 'shell'}:${agentPane.sessionId}`}
          runtimeLogMeta={{
            sessionId: agentPane.sessionId,
            paneId: agentPane.id,
            runtimeId: agentPane.runtimeId,
            paneKind: 'agent',
            isActivePane: activeAgentPaneId === agentPane.id,
            isActiveSession,
            paneCount: paneIds.length,
          }}
          onInput={runtime.handleTerminalInput(agentPane.id)}
          onPointerActivity={() => onTerminalPointerActivity?.(agentPane.runtimeId)}
          onOpenMarkdown={onOpenMarkdown}
          gardenSeeds={gardenSeeds}
          onOpenSeed={onOpenSeed}
          onReady={handleGhosttyTerminalReady(agentPane.id)}
          onResize={runtime.handleTerminalResize(agentPane.id)}
        />
      )}
    </div>
  );
}
