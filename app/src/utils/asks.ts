import type { Ask, AskAnswer, AskQuestion } from '../types/generated';
import type { AskDraft } from '../store/asks';
import { crewDisplayName } from './crewName';

export const ASKS_SETTING = 'asks_enabled';

export function isAsksEnabled(settings: Record<string, string>): boolean {
  return settings[ASKS_SETTING] === 'true';
}

export interface AskHolderSession {
  id: string;
  crewMember?: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
}

export function askBelongsTo(ask: Ask, session: AskHolderSession): boolean {
  if (ask.crew_member) return session.crewMember === ask.crew_member;
  if (ask.chief) return Boolean(session.chiefOfStaff);
  return ask.session_id === session.id;
}

export function askFrom(ask: Ask, holder: (AskHolderSession & { label: string }) | undefined): string {
  if (holder?.chiefOfStaff || ask.chief) return 'Chief';
  if (ask.crew_member) return holder ? crewDisplayName(ask.crew_member) : `${crewDisplayName(ask.crew_member)} (asleep)`;
  return holder?.label ?? 'Closed agent';
}

export function askHolder<S extends AskHolderSession>(ask: Ask, sessions: readonly S[]): S | undefined {
  return sessions.find((session) => askBelongsTo(ask, session));
}

export function askQuestions(ask: Ask): AskQuestion[] {
  return ask.parts.flatMap((part) => (part.question ? [part.question] : []));
}

function waitingSince(ask: Ask, sessions: readonly AskHolderSession[]): string | undefined {
  const holder = askHolder(ask, sessions);
  return holder?.turnOwed ? holder.turnOpenedAt ?? ask.created_at : undefined;
}

export function orderAsks(asks: readonly Ask[], sessions: readonly AskHolderSession[]): Ask[] {
  return [...asks].sort((a, b) => {
    const aWaiting = waitingSince(a, sessions);
    const bWaiting = waitingSince(b, sessions);
    if (Boolean(aWaiting) !== Boolean(bWaiting)) return aWaiting ? -1 : 1;
    return (aWaiting ?? '').localeCompare(bWaiting ?? '') || a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id);
  });
}

export function draftAnswers(ask: Ask, draft: AskDraft): AskAnswer[] {
  return askQuestions(ask).flatMap((question) => {
    const answer = draft.answers[question.key];
    if (!answer) return [];
    const other = answer.other?.trim();
    const text = answer.text?.trim();
    if (answer.picked.length === 0 && !other && !text) return [];
    return [{ key: question.key, picked: answer.picked, ...(other ? { other } : {}), ...(text ? { text } : {}) }];
  });
}

export function draftSendable(ask: Ask, draft: AskDraft): boolean {
  return draftAnswers(ask, draft).length > 0 || draft.note.trim() !== '';
}
