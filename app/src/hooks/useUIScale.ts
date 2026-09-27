import { useState, useEffect, useCallback, useRef } from 'react';
import { useSettings } from '../contexts/SettingsContext';

const SETTINGS_KEY = 'uiScale';
const DEFAULT_SCALE = 1.0;
const MIN_SCALE = 0.7;
const MAX_SCALE = 1.5;
const SCALE_STEP = 0.1;

export function useUIScale() {
  const { settings, setSetting } = useSettings();
  const initializedFromSettings = useRef(false);

  const [scale, setScale] = useState<number>(DEFAULT_SCALE);

  useEffect(() => {
    if (settings[SETTINGS_KEY] && !initializedFromSettings.current) {
      const parsed = parseFloat(settings[SETTINGS_KEY]);
      if (!isNaN(parsed) && parsed >= MIN_SCALE && parsed <= MAX_SCALE) {
        setScale(parsed);
        initializedFromSettings.current = true;
      }
    }
  }, [settings]);

  useEffect(() => {
    document.documentElement.style.setProperty('--ui-scale', scale.toString());
  }, [scale]);

  // Persistence happens in the actions, so a value read from settings is never echoed back.
  const applyScale = useCallback((next: number) => {
    setScale(next);
    setSetting(SETTINGS_KEY, next.toString());
  }, [setSetting]);

  const increaseScale = useCallback(() => {
    applyScale(Math.min(MAX_SCALE, Math.round((scale + SCALE_STEP) * 10) / 10));
  }, [applyScale, scale]);

  const decreaseScale = useCallback(() => {
    applyScale(Math.max(MIN_SCALE, Math.round((scale - SCALE_STEP) * 10) / 10));
  }, [applyScale, scale]);

  const resetScale = useCallback(() => {
    applyScale(DEFAULT_SCALE);
  }, [applyScale]);

  return {
    scale,
    increaseScale,
    decreaseScale,
    resetScale,
  };
}
