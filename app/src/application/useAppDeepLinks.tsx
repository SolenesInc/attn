import { getCurrent, onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { useCallback, useEffect, useRef } from 'react';
import { currentDesktopArrived, useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { type SessionAgent } from '../types/sessionAgent';
interface Options {
  selectAgent: (sessionId: string) => boolean;
  launchAgent: (
    label: string,
    cwd: string,
    providedSessionId?: string,
    agent?: SessionAgent,
    yoloMode?: boolean,
    options?: { chiefOfStaff?: boolean },
  ) => Promise<string>;
}
export function useAppDeepLinks({
  selectAgent,
  launchAgent,
}: Options) {
  const launchingDeepLinks = useRef(new Set<string>());
  const coldStartRead = useRef(false);

  const handleDeepLinkUrl = useCallback(
    (urlStr: string) => {
      if (launchingDeepLinks.current.has(urlStr)) {
        return;
      }

      try {
        const url = new URL(urlStr);
        if (url.host === 'spawn') {
          const cwd = url.searchParams.get('cwd');
          const label = url.searchParams.get('label') || cwd?.split('/').pop() || 'session';
          if (cwd) {
            const currentSessions = useSessionStore.getState().sessions;
            const profileId = useProfilesStore.getState().selectedProfileId;
            const existingSession = currentSessions.find((s) => s.cwd === cwd && s.profileId === profileId);
            if (existingSession) {
              selectAgent(existingSession.id);
            } else {
              launchingDeepLinks.current.add(urlStr);
              void currentDesktopArrived()
                .then(() => launchAgent(label, cwd))
                .catch((error) => {
                  console.error('[DeepLink] spawn failed:', error);
                })
                .finally(() => {
                  launchingDeepLinks.current.delete(urlStr);
                });
            }
          }
        }
      } catch (e) {
        console.error('Failed to parse deep-link URL:', e);
      }
    },
    [launchAgent, selectAgent],
  );

  useEffect(() => {
    if (coldStartRead.current) {
      return;
    }
    coldStartRead.current = true;
    getCurrent()
      .then((urls) => {
        if (urls && urls.length > 0) {
          console.log('[DeepLink] Cold start URLs:', urls);
          for (const urlStr of urls) {
            handleDeepLinkUrl(urlStr);
          }
        }
      })
      .catch((err) => {
        console.error('[DeepLink] getCurrent failed:', err);
      });
  }, [handleDeepLinkUrl]);

  useEffect(() => {
    const unlisten = onOpenUrl((urls) => {
      for (const urlStr of urls) {
        handleDeepLinkUrl(urlStr);
      }
    });

    return () => {
      unlisten.then((fn) => fn());
    };
  }, [handleDeepLinkUrl]);
}
