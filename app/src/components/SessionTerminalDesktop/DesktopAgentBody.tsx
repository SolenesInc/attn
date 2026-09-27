import { AnnotatedTerminal } from '../TerminalAnnotations/AnnotatedTerminal';
import { TerminalStaleBuildNotice } from '../TerminalStaleBuildNotice';
import { useDesktopContext } from './DesktopContext';
import { paneNotice } from './paneNotice';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopAgentBody({ agentPane, paneSession, paneTitle }: DesktopAgentProps) {
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
    onTerminalModelRecovered,
    staleBuildDismissed,
    setStaleBuildDismissed,
    paneIds,
    activeLeafId,
    runtime,
    terminalRefForPane,
    sessionVisible,
    shortcutsEnabled,
    handleGhosttyTerminalReady,
  } = useDesktopContext();
  const notice = paneNotice(agentPane, paneSession, paneTitle);

  return (
    <div className="desktop-pane-body">
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
