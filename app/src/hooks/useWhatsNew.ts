// One-time "what's new" gating; bump WHATS_NEW_ID when there's a new story to tell.

import { useCallback, useEffect, useState } from 'react';

export const WHATS_NEW_ID = 'profiles-desktops-intro-2026-10';
export const WHATS_NEW_STORAGE_KEY = 'attn.whats_new.last_seen';
export const WHATS_NEW_BANNER_STORAGE_KEY = 'attn.whats_new.banner_dismissed';

function readId(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch (err) {
    console.warn(`[whats-new] Failed to read ${key}:`, err);
    return null;
  }
}

function persistId(key: string, id: string): void {
  try {
    window.localStorage.setItem(key, id);
  } catch (err) {
    console.warn(`[whats-new] Failed to persist ${key}:`, err);
  }
}

export interface WhatsNewControls {
  isOpen: boolean;
  /** Re-open the intro on demand (the command palette's What's new). */
  open: () => void;
  /** Close and mark the current release as seen. */
  dismiss: () => void;
  /** Home's replay banner, shown for the current release until dismissed. */
  bannerVisible: boolean;
  dismissBanner: () => void;
}

export function useWhatsNew(): WhatsNewControls {
  const [isOpen, setIsOpen] = useState(false);
  const [bannerVisible, setBannerVisible] = useState(() => readId(WHATS_NEW_BANNER_STORAGE_KEY) !== WHATS_NEW_ID);

  useEffect(() => {
    if (readId(WHATS_NEW_STORAGE_KEY) !== WHATS_NEW_ID) {
      setIsOpen(true);
    }
  }, []);

  const open = useCallback(() => setIsOpen(true), []);

  const dismiss = useCallback(() => {
    setIsOpen(false);
    persistId(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
  }, []);

  const dismissBanner = useCallback(() => {
    setBannerVisible(false);
    persistId(WHATS_NEW_BANNER_STORAGE_KEY, WHATS_NEW_ID);
  }, []);

  return { isOpen, open, dismiss, bannerVisible, dismissBanner };
}
