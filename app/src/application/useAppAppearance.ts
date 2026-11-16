import { useCallback, useEffect, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useKeybindings } from '../contexts/KeybindingsContext';
import { useGardenScale } from '../hooks/useGardenScale';
import { useColors } from '../colors/useColors';
import { terminalAnsiPalette } from '../colors/schemes';
import { useUIScale } from '../hooks/useUIScale';
import { useDaemonStore } from '../store/daemonSessions';
import {
  areSidebarHarnessLogosEnabled,
  SIDEBAR_HARNESS_LOGOS_SETTING,
} from '../utils/sidebarHarnessLogos';
import { AppContentProps } from './appSupport';
import { QUICK_CAPTURE_FONT } from '../quickCapture/client';

interface Options {
  settings: AppContentProps['settings'];
}
export function useAppAppearance({ settings }: Options) {
  const { isReady, sendSetTerminalTheme, sendSetSetting } = useDaemonApi();
  const { repoStates, authorStates } = useDaemonStore();
  const { scale, increaseScale, decreaseScale, resetScale } = useUIScale();
  useEffect(() => {
    const resize = (event: Event) => {
      const change = (event as CustomEvent<'increase' | 'decrease' | 'reset'>).detail;
      ({ increase: increaseScale, decrease: decreaseScale, reset: resetScale })[change]();
    };
    window.addEventListener(QUICK_CAPTURE_FONT, resize);
    return () => window.removeEventListener(QUICK_CAPTURE_FONT, resize);
  }, [increaseScale, decreaseScale, resetScale]);
  const terminalFontSize = Math.round(14 * scale);

  const gardenScale = useGardenScale(scale);

  const { preference: themePreference, colors, setTheme } = useColors();
  const terminalColors = colors.terminal;
  const keybindings = useKeybindings();

  // The daemon worker answers OSC 10/11/12 on the frontend's behalf, so each handshake, first or reconnect, sends the terminal colors in effect.
  useEffect(() => {
    if (!isReady) return;
    sendSetTerminalTheme({
      foreground: terminalColors.fg,
      background: terminalColors.bg,
      cursor: terminalColors.cursor,
      ansi_palette: terminalAnsiPalette(terminalColors),
    });
  }, [isReady, terminalColors, sendSetTerminalTheme]);

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
    terminalColors,
    setTheme,
    keybindings,
    mutedRepos,
    mutedAuthors,
    handleToggleSidebarHarnessLogos,
  };
}
