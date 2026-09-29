interface Overlays {
  locationPickerOpen: boolean;
  whatsNewOpen: boolean;
  settingsOpen: boolean;
  shortcutsOpen: boolean;
  shortcutEditorOpen: boolean;
  actionMenuOpen: boolean;
  snoozeMenuOpen: boolean;
  sessionsOpen: boolean;
  notebookOpen: boolean;
  crewPanelOpen: boolean;
  gardenHoldsWindow: boolean;
  chiefTransferOpen: boolean;
  contextCapOpen: boolean;
  sessionCreationOpen: boolean;
  prLauncherOpen: boolean;
  diagnosticCaptureOpen: boolean;
  markdownOpenerOpen: boolean;
}

export function appOverlayPolicy(overlays: Overlays) {
  const promptOpen = [
    overlays.chiefTransferOpen,
    overlays.contextCapOpen,
    overlays.sessionCreationOpen,
    overlays.prLauncherOpen,
    overlays.diagnosticCaptureOpen,
  ].some(Boolean);
  const libraryOpen = overlays.sessionsOpen || overlays.notebookOpen || overlays.crewPanelOpen;
  const navigationCaptured = [
    overlays.locationPickerOpen,
    overlays.whatsNewOpen,
    overlays.shortcutEditorOpen,
    overlays.actionMenuOpen,
    libraryOpen,
  ].some(Boolean);
  const actionMenuBlocked = [
    promptOpen,
    overlays.settingsOpen,
    overlays.shortcutsOpen,
    overlays.locationPickerOpen,
    overlays.whatsNewOpen,
    libraryOpen,
    overlays.gardenHoldsWindow,
  ].some(Boolean);
  return {
    // Snooze holds focus without suspending the workspace; re-enabling it would
    // steal focus from the row button when the picker closes.
    blockingOverlayOpen: navigationCaptured || actionMenuBlocked,
    workspaceShortcutsEnabled: !overlays.snoozeMenuOpen,
    actionMenuBlocked: actionMenuBlocked || overlays.snoozeMenuOpen,
    appShortcutsEnabled: !navigationCaptured && !overlays.markdownOpenerOpen && !overlays.snoozeMenuOpen,
  };
}
