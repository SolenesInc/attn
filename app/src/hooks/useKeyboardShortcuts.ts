import { useEffect } from 'react';
import type { PaletteMode } from '../components/palette/paletteState';
import { useShortcut } from '../shortcuts/useShortcut';
import { isAccelKeyPressed, isMacLikePlatform } from '../shortcuts/platform';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onNewSessionHorizontal?: () => void;
  onCloseSession: () => void;
  onOpenPalette: (mode: PaletteMode) => void;
  onGoToDashboard: () => void;
  onJumpToWaiting: () => void;
  onNextRun: () => void;
  /** Undefined while the queue arrangement is off; the keystroke is then unbound. */
  onSettleTurn?: () => void;
  onPriority?: () => void;
  onSnoozeTurn?: () => void;
  onCancelCountdown?: () => void;
  onSwitchToDesktopSlot: (slot: number) => void;
  onMoveToDesktopSlot: (slot: number, follow: boolean) => void;
  onOpenDesktopOverview: () => void;
  onSwitchProfile: () => void;
  onPrevSession: () => void;
  onNextSession: () => void;
  onHistoryBack: () => void;
  onHistoryForward: () => void;
  onSelectOrchestrator?: () => void;
  onToggleSidebar?: () => void;
  onShowAgentList: () => void;
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
  blocked?: boolean;
}

export function useKeyboardShortcuts({
  onNewSession,
  onNewSessionHorizontal,
  onCloseSession,
  onOpenPalette,
  onGoToDashboard,
  onJumpToWaiting,
  onNextRun,
  onSettleTurn,
  onPriority,
  onSnoozeTurn,
  onCancelCountdown,
  onSwitchToDesktopSlot,
  onMoveToDesktopSlot,
  onOpenDesktopOverview,
  onSwitchProfile,
  onPrevSession,
  onNextSession,
  onHistoryBack,
  onHistoryForward,
  onSelectOrchestrator,
  onToggleSidebar,
  onShowAgentList,
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
  enabled: navigationEnabled,
  blocked = false,
}: KeyboardShortcutsConfig) {
  const enabled = navigationEnabled && !blocked;
  useShortcut('app.quit', onQuit ?? (() => {}), !blocked && !!onQuit);

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
  useShortcut('session.jumpToWaiting', onJumpToWaiting, enabled);
  useShortcut('session.nextRun', onNextRun, enabled);
  useShortcut('sidebar.agentList', onShowAgentList, enabled);
  useShortcut('session.settle', onSettleTurn ?? (() => {}), enabled && !!onSettleTurn);
  useShortcut('session.priority', onPriority ?? (() => {}), enabled && !!onPriority);
  useShortcut('session.snooze', onSnoozeTurn ?? (() => {}), enabled && !!onSnoozeTurn);
  // Delivered by a native menu item, not the page's keydown listener: AppKit eats
  // ⌘. before the WebView sees it.
  useShortcut('session.cancelCountdown', onCancelCountdown ?? (() => {}), enabled && !!onCancelCountdown);
  useShortcut('session.toggleSidebar', onToggleSidebar ?? (() => {}), enabled && !!onToggleSidebar);
  useShortcut('session.refreshPRs', onRefreshPRs ?? (() => {}), enabled && !!onRefreshPRs);
  useShortcut('desktop.select1', () => onSwitchToDesktopSlot(1), enabled);
  useShortcut('desktop.send1', () => onMoveToDesktopSlot(1, true), enabled);
  useShortcut('desktop.sendStay1', () => onMoveToDesktopSlot(1, false), enabled);
  useShortcut('desktop.select2', () => onSwitchToDesktopSlot(2), enabled);
  useShortcut('desktop.send2', () => onMoveToDesktopSlot(2, true), enabled);
  useShortcut('desktop.sendStay2', () => onMoveToDesktopSlot(2, false), enabled);
  useShortcut('desktop.select3', () => onSwitchToDesktopSlot(3), enabled);
  useShortcut('desktop.send3', () => onMoveToDesktopSlot(3, true), enabled);
  useShortcut('desktop.sendStay3', () => onMoveToDesktopSlot(3, false), enabled);
  useShortcut('desktop.select4', () => onSwitchToDesktopSlot(4), enabled);
  useShortcut('desktop.send4', () => onMoveToDesktopSlot(4, true), enabled);
  useShortcut('desktop.sendStay4', () => onMoveToDesktopSlot(4, false), enabled);
  useShortcut('desktop.select5', () => onSwitchToDesktopSlot(5), enabled);
  useShortcut('desktop.send5', () => onMoveToDesktopSlot(5, true), enabled);
  useShortcut('desktop.sendStay5', () => onMoveToDesktopSlot(5, false), enabled);
  useShortcut('desktop.select6', () => onSwitchToDesktopSlot(6), enabled);
  useShortcut('desktop.send6', () => onMoveToDesktopSlot(6, true), enabled);
  useShortcut('desktop.sendStay6', () => onMoveToDesktopSlot(6, false), enabled);
  useShortcut('desktop.select7', () => onSwitchToDesktopSlot(7), enabled);
  useShortcut('desktop.send7', () => onMoveToDesktopSlot(7, true), enabled);
  useShortcut('desktop.sendStay7', () => onMoveToDesktopSlot(7, false), enabled);
  useShortcut('desktop.select8', () => onSwitchToDesktopSlot(8), enabled);
  useShortcut('desktop.send8', () => onMoveToDesktopSlot(8, true), enabled);
  useShortcut('desktop.sendStay8', () => onMoveToDesktopSlot(8, false), enabled);
  useShortcut('desktop.select9', () => onSwitchToDesktopSlot(9), enabled);
  useShortcut('desktop.send9', () => onMoveToDesktopSlot(9, true), enabled);
  useShortcut('desktop.sendStay9', () => onMoveToDesktopSlot(9, false), enabled);
  useShortcut('dock.attention', onToggleAttentionPanel ?? (() => {}), enabled && !!onToggleAttentionPanel);

  useShortcut('ui.actionMenu', () => onOpenPalette('agents'), !blocked);
  useShortcut('ui.commandPalette', () => onOpenPalette('commands'), !blocked);

  useShortcut('ui.openSettings', onOpenSettings ?? (() => {}), !blocked && !!onOpenSettings);

  useShortcut('ui.showShortcuts', onShowShortcuts ?? (() => {}), !blocked && !!onShowShortcuts);

  useShortcut('ui.increaseFontSize', onIncreaseFontSize ?? (() => {}), !blocked && !!onIncreaseFontSize);
  useShortcut('ui.decreaseFontSize', onDecreaseFontSize ?? (() => {}), !blocked && !!onDecreaseFontSize);
  useShortcut('ui.resetFontSize', onResetFontSize ?? (() => {}), !blocked && !!onResetFontSize);

  useShortcut('file.open', onOpenFile ?? (() => {}), enabled && !!onOpenFile);

  useShortcut('notebook.openTile', onOpenNotebookTile ?? (() => {}), enabled && !!onOpenNotebookTile);
  useShortcut('notebook.openFullscreen', onOpenNotebookFullscreen ?? (() => {}), enabled && !!onOpenNotebookFullscreen);

  useShortcut('board.open', onOpenGarden ?? (() => {}), !blocked && (gardenShortcutEnabled ?? enabled) && !!onOpenGarden);

  useShortcut('sessions.open', onOpenSessions ?? (() => {}), !blocked && !!onOpenSessions);

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
