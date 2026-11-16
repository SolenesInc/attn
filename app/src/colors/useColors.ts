import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from 'react';
import { useSettings } from '../contexts/SettingsContext';
import {
  applyColors,
  COLOR_OVERRIDES_SETTING,
  COLOR_SCHEME_SETTING,
  resolveColors,
  systemAppearance,
  THEME_SETTING,
  themePreference,
  type ThemePreference,
} from './resolve';
import type { AppearanceName } from './schemes';

function useSystemAppearance(follow: boolean): AppearanceName {
  const [appearance, setAppearance] = useState(systemAppearance);
  useEffect(() => {
    if (!follow) return;
    setAppearance(systemAppearance());
    const media = window.matchMedia('(prefers-color-scheme: light)');
    const change = () => setAppearance(systemAppearance());
    media.addEventListener('change', change);
    return () => media.removeEventListener('change', change);
  }, [follow]);
  return appearance;
}

export function useAppearance(): { preference: ThemePreference; appearance: AppearanceName } {
  const { settings } = useSettings();
  const preference = themePreference(settings);
  const system = useSystemAppearance(preference === 'system');
  return { preference, appearance: preference === 'system' ? system : preference };
}

export function useColors() {
  const { settings, setSetting } = useSettings();
  const { preference, appearance } = useAppearance();
  const scheme = settings[COLOR_SCHEME_SETTING];
  const overrides = settings[COLOR_OVERRIDES_SETTING];

  const colors = useMemo(() => resolveColors(scheme, overrides, appearance), [scheme, overrides, appearance]);
  useLayoutEffect(() => applyColors(colors), [colors]);

  const setTheme = useCallback((value: ThemePreference) => setSetting(THEME_SETTING, value), [setSetting]);

  return { preference, colors, setTheme };
}
