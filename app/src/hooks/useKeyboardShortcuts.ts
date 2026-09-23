import { useEffect } from 'react';
import { useShortcut } from '../shortcuts/useShortcut';
import { isAccelKeyPressed, isMacLikePlatform } from '../shortcuts/platform';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onNewSessionHorizontal?: () => void;
  onCloseSession: () => void;
  onToggleActionMenu: () => void;
  onGoToDashboard: () => void;
  onToggleGridMode?: () => void;
  onJumpToWaiting: () => void;
  /** Undefined while the queue arrangement is off; the keystroke is then unbound. */
  onSettleTurn?: () => void;
  onSnoozeTurn?: () => void;
  onCancelCountdown?: () => void;
  onSwitchToDesktopSlot: (slot: number) => void;
  onSendToDesktopSlot: (slot: number) => void;
  onOpenDesktopOverview: () => void;
  onSwitchProfile: () => void;
  onPrevSession: () => void;
  onNextSession: () => void;
  onHistoryBack: () => void;
  onHistoryForward: () => void;
  onSelectOrchestrator?: () => void;
  onToggleSidebar?: () => void;
  onRefreshPRs?: () => void;
  onToggleAttentionPanel?: () => void;
  onOpenSettings?: () => void;
  onShowShortcuts?: () => void;
  onIncreaseFontSize?: () => void;
  onDecreaseFontSize?: () => void;
  onResetFontSize?: () => void;
  onOpenFile?: () => void;
  onOpenNotebookTile?: () => void;
  onOpenNotebookFullscreen?: () => void;
  onOpenGarden?: () => void;
  /** Keep the Garden shortcut live fullscreen so the same key can switch frames. */
  gardenShortcutEnabled?: boolean;
  onOpenSessions?: () => void;
  onQuit?: () => void;
  enabled: boolean;
}

export function useKeyboardShortcuts({
  onNewSession,
  onNewSessionHorizontal,
  onCloseSession,
  onToggleActionMenu,
  onGoToDashboard,
  onToggleGridMode,
  onJumpToWaiting,
  onSettleTurn,
  onSnoozeTurn,
  onCancelCountdown,
  onSwitchToDesktopSlot,
  onSendToDesktopSlot,
  onOpenDesktopOverview,
  onSwitchProfile,
  onPrevSession,
  onNextSession,
  onHistoryBack,
  onHistoryForward,
  onSelectOrchestrator,
  onToggleSidebar,
  onRefreshPRs,
  onToggleAttentionPanel,
  onOpenSettings,
  onShowShortcuts,
  onIncreaseFontSize,
  onDecreaseFontSize,
  onResetFontSize,
  onOpenFile,
  onOpenNotebookTile,
  onOpenNotebookFullscreen,
  onOpenGarden,
  gardenShortcutEnabled,
  onOpenSessions,
  onQuit,
  enabled,
}: KeyboardShortcutsConfig) {
  useShortcut('app.quit', onQuit ?? (() => {}), !!onQuit);

  useShortcut('session.new', onNewSession, enabled);
  useShortcut('session.newHorizontal', onNewSessionHorizontal ?? (() => {}), enabled && !!onNewSessionHorizontal);
  useShortcut('session.close', onCloseSession, enabled);
  useShortcut('session.prev', onPrevSession, enabled);
  useShortcut('session.next', onNextSession, enabled);
  useShortcut('session.historyBack', onHistoryBack, enabled);
  useShortcut('session.historyForward', onHistoryForward, enabled);
  useShortcut('session.orchestrator', onSelectOrchestrator ?? (() => {}), enabled && !!onSelectOrchestrator);
  useShortcut('session.goToDashboard', onGoToDashboard, enabled);
  useShortcut('desktop.overview', onOpenDesktopOverview, enabled);
  useShortcut('profile.switch', onSwitchProfile, enabled);
  useShortcut('view.toggleGrid', onToggleGridMode ?? (() => {}), enabled && !!onToggleGridMode);
  useShortcut('session.jumpToWaiting', onJumpToWaiting, enabled);
  useShortcut('session.settle', onSettleTurn ?? (() => {}), enabled && !!onSettleTurn);
  useShortcut('session.snooze', onSnoozeTurn ?? (() => {}), enabled && !!onSnoozeTurn);
  // Delivered by a native menu item, not the page's keydown listener: AppKit eats
  // ⌘. before the WebView sees it.
  useShortcut('session.cancelCountdown', onCancelCountdown ?? (() => {}), enabled && !!onCancelCountdown);
  useShortcut('session.toggleSidebar', onToggleSidebar ?? (() => {}), enabled && !!onToggleSidebar);
  useShortcut('session.refreshPRs', onRefreshPRs ?? (() => {}), enabled && !!onRefreshPRs);
  useShortcut('desktop.select1', () => onSwitchToDesktopSlot(1), enabled);
  useShortcut('desktop.send1', () => onSendToDesktopSlot(1), enabled);
  useShortcut('desktop.select2', () => onSwitchToDesktopSlot(2), enabled);
  useShortcut('desktop.send2', () => onSendToDesktopSlot(2), enabled);
  useShortcut('desktop.select3', () => onSwitchToDesktopSlot(3), enabled);
  useShortcut('desktop.send3', () => onSendToDesktopSlot(3), enabled);
  useShortcut('desktop.select4', () => onSwitchToDesktopSlot(4), enabled);
  useShortcut('desktop.send4', () => onSendToDesktopSlot(4), enabled);
  useShortcut('desktop.select5', () => onSwitchToDesktopSlot(5), enabled);
  useShortcut('desktop.send5', () => onSendToDesktopSlot(5), enabled);
  useShortcut('desktop.select6', () => onSwitchToDesktopSlot(6), enabled);
  useShortcut('desktop.send6', () => onSendToDesktopSlot(6), enabled);
  useShortcut('desktop.select7', () => onSwitchToDesktopSlot(7), enabled);
  useShortcut('desktop.send7', () => onSendToDesktopSlot(7), enabled);
  useShortcut('desktop.select8', () => onSwitchToDesktopSlot(8), enabled);
  useShortcut('desktop.send8', () => onSendToDesktopSlot(8), enabled);
  useShortcut('desktop.select9', () => onSwitchToDesktopSlot(9), enabled);
  useShortcut('desktop.send9', () => onSendToDesktopSlot(9), enabled);
  useShortcut('dock.attention', onToggleAttentionPanel ?? (() => {}), enabled && !!onToggleAttentionPanel);

  useShortcut('ui.actionMenu', onToggleActionMenu, true);

  useShortcut('ui.openSettings', onOpenSettings ?? (() => {}), !!onOpenSettings);

  useShortcut('ui.showShortcuts', onShowShortcuts ?? (() => {}), !!onShowShortcuts);

  useShortcut('ui.increaseFontSize', onIncreaseFontSize ?? (() => {}), !!onIncreaseFontSize);
  useShortcut('ui.decreaseFontSize', onDecreaseFontSize ?? (() => {}), !!onDecreaseFontSize);
  useShortcut('ui.resetFontSize', onResetFontSize ?? (() => {}), !!onResetFontSize);

  useShortcut('file.open', onOpenFile ?? (() => {}), enabled && !!onOpenFile);

  useShortcut('notebook.openTile', onOpenNotebookTile ?? (() => {}), enabled && !!onOpenNotebookTile);
  useShortcut('notebook.openFullscreen', onOpenNotebookFullscreen ?? (() => {}), enabled && !!onOpenNotebookFullscreen);

  useShortcut('board.open', onOpenGarden ?? (() => {}), (gardenShortcutEnabled ?? enabled) && !!onOpenGarden);

  useShortcut('sessions.open', onOpenSessions ?? (() => {}), !!onOpenSessions);

  useEffect(() => {
    const preventWindowCloseShortcut = (e: KeyboardEvent) => {
      if (!isMacLikePlatform() || !isAccelKeyPressed(e) || e.shiftKey || e.altKey) {
        return;
      }
      if (e.key.toLowerCase() !== 'w') {
        return;
      }
      // Keep Cmd+W inside the app so shortcut handlers can decide
      // whether to close a pane, close a session, or do nothing.
      e.preventDefault();
    };

    window.addEventListener('keydown', preventWindowCloseShortcut, true);
    return () => {
      window.removeEventListener('keydown', preventWindowCloseShortcut, true);
    };
  }, []);

}
