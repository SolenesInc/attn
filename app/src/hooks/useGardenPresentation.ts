import { useCallback, useState } from 'react';

export type GardenMode = 'closed' | 'dock' | 'full';
export type GardenOpenMode = Exclude<GardenMode, 'closed'>;
export type GardenView = 'list' | 'board';

export const GARDEN_FRAME_MODE_STORAGE_KEY = 'attn.garden.frame';
export const GARDEN_FULLSCREEN_VIEW_STORAGE_KEY = 'attn.garden.fullscreenView';

function readChoice<T extends string>(key: string, choices: readonly T[], fallback: T): T {
  try {
    const stored = window.localStorage.getItem(key);
    return choices.includes(stored as T) ? stored as T : fallback;
  } catch {
    return fallback;
  }
}

function writeChoice(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // The preference is optional; the current app session still keeps the choice.
  }
}

export function readGardenFrameMode(): GardenOpenMode {
  return readChoice(GARDEN_FRAME_MODE_STORAGE_KEY, ['dock', 'full'], 'dock');
}

export function readGardenFullscreenView(): GardenView {
  return readChoice(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY, ['list', 'board'], 'list');
}

export function useGardenFullscreenView() {
  const [view, setViewState] = useState<GardenView>(readGardenFullscreenView);
  const setView = useCallback((next: GardenView) => {
    setViewState(next);
    writeChoice(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY, next);
  }, []);
  return [view, setView] as const;
}

interface GardenPresentationOptions {
  dockOpen: boolean;
  openDock: () => void;
  closeDock: () => void;
}

export function useGardenPresentation({ dockOpen, openDock, closeDock }: GardenPresentationOptions) {
  const [holdsWindow, setHoldsWindow] = useState(false);
  const [lastMode, setLastMode] = useState<GardenOpenMode>(readGardenFrameMode);
  const mode: GardenMode = holdsWindow ? 'full' : dockOpen ? 'dock' : 'closed';

  const show = useCallback((next: GardenOpenMode) => {
    if (next === 'full') {
      setHoldsWindow(true);
      return;
    }
    setHoldsWindow(false);
    openDock();
  }, [openDock]);

  const remember = useCallback((next: GardenOpenMode) => {
    setLastMode(next);
    writeChoice(GARDEN_FRAME_MODE_STORAGE_KEY, next);
  }, []);

  const open = useCallback(() => {
    show(lastMode);
  }, [lastMode, show]);

  const toggleFrame = useCallback(() => {
    if (mode === 'closed') {
      show(lastMode);
      return;
    }
    const next = mode === 'full' ? 'dock' : 'full';
    remember(next);
    show(next);
  }, [lastMode, mode, remember, show]);

  const close = useCallback(() => {
    setHoldsWindow(false);
    closeDock();
  }, [closeDock]);

  const toggleFromIcon = useCallback(() => {
    if (mode === 'closed') {
      open();
      return;
    }
    close();
  }, [close, mode, open]);

  return {
    mode,
    holdsWindow,
    lastMode,
    open,
    toggleFrame,
    toggleFromIcon,
    close,
  };
}
