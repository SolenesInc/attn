interface Overlays {
  locationPickerOpen: boolean;
  whatsNewOpen: boolean;
  settingsOpen: boolean;
  shortcutsOpen: boolean;
  shortcutEditorOpen: boolean;
  actionMenuOpen: boolean;
  sessionsOpen: boolean;
  notebookOpen: boolean;
  crewPanelOpen: boolean;
  gardenHoldsWindow: boolean;
  chiefTransferOpen: boolean;
  contextCapOpen: boolean;
  appViewParamsOpen: boolean;
  sessionCloseOpen: boolean;
  sessionCreationOpen: boolean;
  prLauncherOpen: boolean;
  diagnosticCaptureOpen: boolean;
  markdownOpenerOpen: boolean;
}

export function appOverlayPolicy(overlays: Overlays) {
  const promptOpen = [
    overlays.chiefTransferOpen,
    overlays.contextCapOpen,
    overlays.appViewParamsOpen,
    overlays.sessionCloseOpen,
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
    blockingOverlayOpen: navigationCaptured || actionMenuBlocked,
    actionMenuBlocked,
    appShortcutsEnabled: !navigationCaptured && !overlays.markdownOpenerOpen,
  };
}
