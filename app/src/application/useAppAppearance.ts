import { useCallback, useEffect, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useKeybindings } from '../contexts/KeybindingsContext';
import { useGardenScale } from '../hooks/useGardenScale';
import { useTheme } from '../hooks/useTheme';
import { useUIScale } from '../hooks/useUIScale';
import { useDaemonStore } from '../store/daemonSessions';
import {
  areSidebarHarnessLogosEnabled,
  SIDEBAR_HARNESS_LOGOS_SETTING,
} from '../utils/sidebarHarnessLogos';
import { getTerminalAnsiPaletteColors, getTerminalTheme } from '../utils/terminalSizing';
import { AppContentProps } from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
}
export function useAppAppearance({ settings }: Options) {
  const { hasReceivedInitialState, sendSetTerminalTheme, sendSetSetting } = useDaemonApi();
  const { repoStates, authorStates } = useDaemonStore();
  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  const terminalFontSize = Math.round(14 * scale);

  const gardenScale = useGardenScale(scale);

  const { preference: themePreference, resolved: resolvedTheme, setTheme } = useTheme();
  const keybindings = useKeybindings();

  // The daemon worker answers OSC 10/11/12 on the frontend's behalf, so it needs the resolved theme; hasReceivedInitialState doubles as the reconnect signal.
  useEffect(() => {
    if (!hasReceivedInitialState) return;
    const theme = getTerminalTheme(resolvedTheme);
    sendSetTerminalTheme({
      foreground: theme.foreground,
      background: theme.background,
      cursor: theme.cursor,
      ansi_palette: getTerminalAnsiPaletteColors(resolvedTheme),
    });
  }, [hasReceivedInitialState, resolvedTheme, sendSetTerminalTheme]);

  const mutedRepos = useMemo(
    () => repoStates.filter((r) => r.muted).map((r) => r.repo),
    [repoStates],
  );
  const mutedAuthors = useMemo(
    () => authorStates.filter((a) => a.muted).map((a) => a.author),
    [authorStates],
  );
  const handleToggleSidebarHarnessLogos = useCallback(() => {
    sendSetSetting(
      SIDEBAR_HARNESS_LOGOS_SETTING,
      areSidebarHarnessLogosEnabled(settings) ? 'false' : 'true',
    );
  }, [sendSetSetting, settings]);

  return {
    scale,
    increaseScale,
    decreaseScale,
    resetScale,
    terminalFontSize,
    gardenScale,
    themePreference,
    resolvedTheme,
    setTheme,
    keybindings,
    mutedRepos,
    mutedAuthors,
    handleToggleSidebarHarnessLogos,
  };
}
