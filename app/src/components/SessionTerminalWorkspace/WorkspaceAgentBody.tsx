import { AnnotatedTerminal } from '../TerminalAnnotations/AnnotatedTerminal';
import { TerminalStaleBuildNotice } from '../TerminalStaleBuildNotice';
import { useWorkspaceContext } from './WorkspaceContext';
import { paneNotice } from './paneNotice';
import type { WorkspaceAgentProps } from './workspaceTypes';

export function WorkspaceAgentBody({ agentPane, paneSession, paneTitle }: WorkspaceAgentProps) {
  const {
    workspaceId,
    gardenSeeds,
    onOpenSeed,
    annotationApi,
    activePaneId,
    fontSize,
    resolvedTheme,
    isActiveSession,
    terminalsLive,
    onTerminalPointerActivity,
    onOpenMarkdown,
    onTerminalModelRecovered,
    staleBuildDismissed,
    setStaleBuildDismissed,
    paneIds,
    activeLeafId,
    runtime,
    terminalRefForPane,
    sessionVisible,
    handleGhosttyTerminalReady,
  } = useWorkspaceContext();
  const notice = paneNotice(agentPane, paneSession, paneTitle);

  return (
    <div className="workspace-pane-body">
      {paneSession?.terminalBuildStale && !staleBuildDismissed.has(agentPane.sessionId) ? (
        <TerminalStaleBuildNotice
          onDismiss={() => setStaleBuildDismissed((prev) => new Set(prev).add(agentPane.sessionId))}
        />
      ) : null}
      {notice ? (
        <div className={`workspace-pane-status workspace-pane-status--${notice.tone}`}>
          <span className="workspace-pane-status-spinner" aria-hidden="true" />
          <span>{notice.text}</span>
        </div>
      ) : !terminalsLive ? (
        <div
          className="workspace-pane-virtualized"
          aria-hidden="true"
          data-testid={`pane-virtualized-${agentPane.id}`}
        />
      ) : (
        <AnnotatedTerminal
          ref={terminalRefForPane(agentPane.id)}
          workspaceId={workspaceId}
          sessionId={agentPane.sessionId}
          annotationApi={annotationApi}
          // At most one pane owns ⌘Enter for the annotation send shortcut.
          paneActive={isActiveSession && sessionVisible && activeLeafId === agentPane.id}
          fontSize={fontSize}
          resolvedTheme={resolvedTheme}
          cwd={paneSession?.cwd}
          debugName={`agent:${paneTitle}:${paneSession?.agent ?? 'shell'}:${agentPane.sessionId}`}
          runtimeLogMeta={{
            sessionId: agentPane.sessionId,
            paneId: agentPane.id,
            runtimeId: agentPane.runtimeId,
            paneKind: 'agent',
            isActivePane: activePaneId === agentPane.id,
            isActiveSession,
            paneCount: paneIds.length,
          }}
          onInput={runtime.handleTerminalInput(agentPane.id)}
          onPointerActivity={() => onTerminalPointerActivity?.(agentPane.sessionId)}
          onOpenMarkdown={onOpenMarkdown}
          gardenSeeds={gardenSeeds}
          onOpenSeed={onOpenSeed}
          onReady={handleGhosttyTerminalReady(agentPane.id)}
          onResize={runtime.handleTerminalResize(agentPane.id)}
          onTerminalModelRecovered={onTerminalModelRecovered}
        />
      )}
    </div>
  );
}
