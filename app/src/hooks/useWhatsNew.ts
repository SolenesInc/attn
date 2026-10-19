// One-time "what's new" gating; bump WHATS_NEW_ID when there's a new story to tell.

import { useCallback, useEffect, useState } from 'react';
import { FirstDay } from '../types/generated';

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

export function useWhatsNew(firstDay: FirstDay | undefined): WhatsNewControls {
  const [opening, setOpening] = useState<'closed' | 'automatic' | 'requested'>('closed');
  const [bannerVisible, setBannerVisible] = useState(() => readId(WHATS_NEW_BANNER_STORAGE_KEY) !== WHATS_NEW_ID);

  useEffect(() => {
    if (firstDay === undefined) return;
    if (firstDay === FirstDay.Full) {
      persistId(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
      persistId(WHATS_NEW_BANNER_STORAGE_KEY, WHATS_NEW_ID);
      setOpening(current => current === 'automatic' ? 'closed' : current);
      setBannerVisible(false);
    } else if (readId(WHATS_NEW_STORAGE_KEY) !== WHATS_NEW_ID) {
      setOpening(current => current === 'closed' ? 'automatic' : current);
    }
  }, [firstDay]);

  const open = useCallback(() => setOpening('requested'), []);

  const dismiss = useCallback(() => {
    setOpening('closed');
    persistId(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
  }, []);

  const dismissBanner = useCallback(() => {
    setBannerVisible(false);
    persistId(WHATS_NEW_BANNER_STORAGE_KEY, WHATS_NEW_ID);
  }, []);

  const showRelease = firstDay !== undefined && firstDay !== FirstDay.Full;
  return { isOpen: opening === 'requested' || (showRelease && opening === 'automatic'), open, dismiss, bannerVisible: showRelease && bannerVisible, dismissBanner };
}
