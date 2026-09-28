import { useMemo } from 'react';
import { useSessionBehindScreen } from '../hooks/useDesktopSelectionBridge';
import { useSessionStore, type Session } from '../store/sessions';
import { useAppInputs } from './AppContexts';
import type { AppContentProps } from './appSupport';

export interface ScreenSession {
  session: Session;
  endpoint: AppContentProps['daemonEndpoints'][number] | null;
}

// Everything derived from the session behind the screen comes from here.
export function useScreenSession(): ScreenSession | null {
  const sessionId = useSessionBehindScreen();
  const session = useSessionStore((state) => state.sessions.find((entry) => entry.id === sessionId) ?? null);
  const { daemonEndpoints } = useAppInputs();
  return useMemo(() => {
    if (!session) return null;
    const endpoint = session.endpointId ? daemonEndpoints.find((entry) => entry.id === session.endpointId) ?? null : null;
    return { session, endpoint };
  }, [daemonEndpoints, session]);
}

export function localDirectoryOf(session: Pick<Session, 'cwd' | 'endpointId'> | null | undefined): string | undefined {
  return session && !session.endpointId ? session.cwd : undefined;
}
