interface Overlays {
  locationPickerOpen: boolean;
  whatsNewOpen: boolean;
  settingsOpen: boolean;
  shortcutsOpen: boolean;
  shortcutEditorOpen: boolean;
  paletteOpen: boolean;
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
  desktopOverviewOpen: boolean;
  profileSwitcherOpen: boolean;
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
    overlays.paletteOpen,
    overlays.desktopOverviewOpen,
    overlays.profileSwitcherOpen,
    libraryOpen,
  ].some(Boolean);
  const paletteBlocked = [
    promptOpen,
    overlays.settingsOpen,
    overlays.shortcutsOpen,
    overlays.locationPickerOpen,
    overlays.whatsNewOpen,
    libraryOpen,
    overlays.gardenHoldsWindow,
  ].some(Boolean);
  const blockingOverlayOpen = navigationCaptured || paletteBlocked;
  return {
    blockingOverlayOpen,
    windowCovered: blockingOverlayOpen || overlays.markdownOpenerOpen,
    paletteBlocked,
    appShortcutsEnabled: !navigationCaptured && !overlays.markdownOpenerOpen,
  };
}
