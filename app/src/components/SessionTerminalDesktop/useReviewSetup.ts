import { useContext, useMemo } from 'react';
import { DesktopsContext } from '../../application/AppContexts';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { useSettings } from '../../contexts/SettingsContext';
import { useSessionStore, type Session } from '../../store/sessions';
import type { ReviewBundle } from '../../types/generated';
import { sessionDisplayName } from '../../utils/sessionDisplayName';
import type { ReviewSetup } from '../ReviewReader/ReviewKeys';

const SETUP_RUNNING = new Set<string>(['launching', 'working', 'pending_approval']);

/** The labels of the review's setup sessions still at work, as one JSON string so the store's other changes leave it equal. */
function runningSetupLabels(sessions: readonly Session[], setupSessions: string): string {
  const labels: Record<string, string> = {};
  if (setupSessions) {
    for (const id of setupSessions.split('\n')) {
      const session = sessions.find((candidate) => candidate.id === id);
      if (session && SETUP_RUNNING.has(session.state)) labels[id] = sessionDisplayName(session);
    }
  }
  return JSON.stringify(labels);
}

export function setupOfferSettingKey(repository: string, host?: string): string {
  return `code_review_setup_offer_${host || 'local'}_${repository}`;
}

/** Offers a review's missing language servers for setup by an agent on the review's machine. */
export function useReviewSetup(desktopId: string, document: string, host: string | undefined, review: ReviewBundle | undefined): ReviewSetup | undefined {
  const sendCodeNavSetup = useOptionalDaemonApi()?.sendCodeNavSetup;
  const { settings, setSetting } = useSettings();
  const setupSessions = review?.navigation.flatMap((entry) => (entry.setup_session_id ? [entry.setup_session_id] : [])).join('\n') ?? '';
  const runningLabels = useSessionStore((state) => runningSetupLabels(state.sessions, setupSessions));
  const selectSession = useContext(DesktopsContext)?.navigation.handleSelectSession;
  const repository = review?.repository ?? '';
  const offerKey = setupOfferSettingKey(repository, host);
  const offered = settings[offerKey] !== 'off';
  return useMemo<ReviewSetup | undefined>(() => (sendCodeNavSetup && repository
    ? {
        start: (navigation) => sendCodeNavSetup({
          desktop_id: desktopId,
          ...(host ? { host } : {}),
          document,
          repository,
          language: navigation.language,
          file: navigation.file,
          state: navigation.state,
          ...(navigation.detail ? { detail: navigation.detail } : {}),
        }).then(
          (result) => (result.success ? null : result.error ?? 'the daemon gave no reason'),
          (error: unknown) => (error instanceof Error ? error.message : String(error)),
        ),
        runningSessionLabel: (sessionId) => (JSON.parse(runningLabels) as Record<string, string>)[sessionId],
        openSession: (sessionId) => selectSession?.(sessionId),
        offered,
        setOffered: (next) => setSetting(offerKey, next ? '' : 'off'),
      }
    : undefined), [desktopId, document, host, offerKey, offered, repository, runningLabels, selectSession, sendCodeNavSetup, setSetting]);
}
