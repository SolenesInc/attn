import { useCallback, useMemo, useState } from 'react';
import type { CrewMember } from '../types/generated';
import type { CrewMutationOutcome } from './daemonCrewEvents';
import type { CrewRestartOptions } from './useDaemonSocket';

export interface CrewRestartAttempt {
  requestId: string;
  priorRequestId?: string;
  expectedSessionId: string;
  expectedRevision: number;
  sending: boolean;
  delivery?: number;
  transportError?: string;
  conflict?: boolean;
}

export interface CrewRestarts {
  read(member: CrewMember): CrewRestartAttempt | undefined;
  start(member: CrewMember): void;
  resend(member: CrewMember): void;
  discard(member: string): void;
}

function currentAttempt(member: CrewMember, attempt?: CrewRestartAttempt): CrewRestartAttempt | undefined {
  const authoritativeRequestId = member.restart?.request_id;
  if (!attempt || !authoritativeRequestId) return attempt;
  if (authoritativeRequestId === attempt.requestId || authoritativeRequestId === attempt.priorRequestId) return attempt;
  return undefined;
}

export function useCrewRestart(
  send: (request: CrewRestartOptions) => Promise<CrewMutationOutcome>,
  observe: (member: CrewMember) => void,
): CrewRestarts {
  const [attempts, setAttempts] = useState<Record<string, CrewRestartAttempt>>({});

  const deliver = useCallback((member: string, previous: CrewRestartAttempt) => {
    const attempt = { ...previous, delivery: (previous.delivery ?? 0) + 1 };
    const finish = (result: Pick<CrewRestartAttempt, 'transportError' | 'conflict'>) => {
      setAttempts((current) => {
        const live = current[member];
        if (live?.requestId !== attempt.requestId || live.delivery !== attempt.delivery) return current;
        return { ...current, [member]: { ...attempt, sending: false, ...result } };
      });
    };
    setAttempts((current) => ({ ...current, [member]: { ...attempt, sending: true, transportError: undefined, conflict: false } }));
    void send({
      member,
      requestId: attempt.requestId,
      expectedSessionId: attempt.expectedSessionId,
      expectedRevision: attempt.expectedRevision,
    }).then((outcome) => {
      if (outcome.member) observe(outcome.member);
      if (outcome.success) {
        finish({});
        return;
      }
      finish({ transportError: outcome.error || 'The restart request failed.', conflict: outcome.conflict });
    }).catch((error) => {
      finish({ transportError: error instanceof Error ? error.message : String(error) });
    });
  }, [observe, send]);

  const start = useCallback((member: CrewMember) => {
    deliver(member.id, {
      requestId: crypto.randomUUID(),
      priorRequestId: member.restart?.request_id,
      expectedSessionId: member.binding_session ?? '',
      expectedRevision: member.revision,
      sending: false,
    });
  }, [deliver]);

  const read = useCallback((member: CrewMember) => currentAttempt(member, attempts[member.id]), [attempts]);

  const resend = useCallback((member: CrewMember) => {
    const attempt = read(member);
    if (attempt) deliver(member.id, attempt);
  }, [deliver, read]);

  const discard = useCallback((member: string) => {
    setAttempts((current) => {
      if (!current[member]) return current;
      const next = { ...current };
      delete next[member];
      return next;
    });
  }, []);

  return useMemo(() => ({ read, start, resend, discard }), [discard, read, resend, start]);
}
