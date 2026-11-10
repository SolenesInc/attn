import { useContext, useMemo } from 'react';
import { DesktopsContext } from '../../application/AppContexts';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { useSettings } from '../../contexts/SettingsContext';
import { useSessionStore } from '../../store/sessions';
import type { ReviewBundle } from '../../types/generated';
import { sessionDisplayName } from '../../utils/sessionDisplayName';
import type { ReviewSetup } from '../ReviewReader/ReviewKeys';

export function setupOfferSettingKey(repository: string, host?: string): string {
  return `code_review_setup_offer_${host || 'local'}_${repository}`;
}

/** Offers a review's missing language servers for setup by an agent on the review's machine. */
export function useReviewSetup(desktopId: string, document: string, host: string | undefined, review: ReviewBundle | undefined): ReviewSetup | undefined {
  const sendCodeNavSetup = useOptionalDaemonApi()?.sendCodeNavSetup;
  const { settings, setSetting } = useSettings();
  const sessions = useSessionStore((state) => state.sessions);
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
        sessionLabel: (sessionId) => {
          const session = sessions.find((candidate) => candidate.id === sessionId);
          return session ? sessionDisplayName(session) : undefined;
        },
        openSession: (sessionId) => selectSession?.(sessionId),
        offered,
        setOffered: (next) => setSetting(offerKey, next ? '' : 'off'),
      }
    : undefined), [desktopId, document, host, offerKey, offered, repository, selectSession, sendCodeNavSetup, sessions, setSetting]);
}
