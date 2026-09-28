import { getCurrent, onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { useCallback, useEffect, useRef } from 'react';
import { currentDesktopArrived } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { type SessionAgent } from '../types/sessionAgent';
interface Options {
  selectAgent: (sessionId: string) => boolean;
  launchAgent: (
    label: string,
    cwd: string,
    providedSessionId?: string,
    agent?: SessionAgent,
    endpointId?: string,
    yoloMode?: boolean,
    options?: { chiefOfStaff?: boolean; autoMode?: boolean },
  ) => Promise<string>;
}
export function useAppDeepLinks({
  selectAgent,
  launchAgent,
}: Options) {
  const handledDeepLinks = useRef(new Set<string>());

  const handleDeepLinkUrl = useCallback(
    (urlStr: string) => {
      if (handledDeepLinks.current.has(urlStr)) {
        return;
      }

      try {
        const url = new URL(urlStr);
        if (url.host === 'spawn') {
          const cwd = url.searchParams.get('cwd');
          const label = url.searchParams.get('label') || cwd?.split('/').pop() || 'session';
          if (cwd) {
            const currentSessions = useSessionStore.getState().sessions;
            const existingSession = currentSessions.find((s) => s.cwd === cwd);
            if (existingSession) {
              handledDeepLinks.current.add(urlStr);
              selectAgent(existingSession.id);
            } else {
              handledDeepLinks.current.add(urlStr);
              void currentDesktopArrived()
                .then(() => launchAgent(label, cwd))
                .catch((error) => {
                  handledDeepLinks.current.delete(urlStr);
                  console.error('[DeepLink] spawn failed:', error);
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
