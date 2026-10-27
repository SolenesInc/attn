import type { Ask, AskAnswer, AskQuestion, CrewMember, Seed } from '../types/generated';
import type { AskDraft } from '../store/asks';

export const ASKS_SETTING = 'asks_enabled';

export function isAsksEnabled(settings: Record<string, string>): boolean {
  return settings[ASKS_SETTING] === 'true';
}

export interface AskHolderSession {
  id: string;
  crewMember?: string;
  seedId?: string;
  turnOwed?: boolean;
  turnOpenedAt?: string;
}

function tendsSeed(session: AskHolderSession, seedId: string, seeds: readonly Seed[]): boolean {
  const seed = seeds.find((candidate) => candidate.id === seedId);
  if (!seed) return session.seedId === seedId;
  const tender = seed.tender?.ref ?? '';
  if (tender.startsWith('member:')) return session.crewMember === tender.slice('member:'.length);
  return tender !== '' && tender === `session:${session.id}`;
}

export function askBelongsTo(ask: Ask, session: AskHolderSession, seeds: readonly Seed[]): boolean {
  if (ask.seed_id) return tendsSeed(session, ask.seed_id, seeds);
  if (ask.crew_member) return session.crewMember === ask.crew_member;
  return ask.session_id === session.id;
}

export function askFrom(
  ask: Ask,
  holder: (AskHolderSession & { label: string }) | undefined,
  crew: readonly Pick<CrewMember, 'key' | 'name'>[],
): string {
  if (ask.crew_member) {
    const name = crew.find((member) => member.key === ask.crew_member)?.name ?? ask.crew_member;
    return holder ? name : `${name} (asleep)`;
  }
  if (!holder && ask.seed_id) return `waiting for the next tender of ${ask.seed_title || ask.seed_id}`;
  return holder?.label ?? 'Closed agent';
}

export function askHolder<S extends AskHolderSession>(ask: Ask, sessions: readonly S[], seeds: readonly Seed[]): S | undefined {
  return sessions.find((session) => askBelongsTo(ask, session, seeds));
}

export function askQuestions(ask: Ask): AskQuestion[] {
  return ask.parts.flatMap((part) => (part.question ? [part.question] : []));
}

function waitingSince(ask: Ask, sessions: readonly AskHolderSession[], seeds: readonly Seed[]): string | undefined {
  const holder = askHolder(ask, sessions, seeds);
  return holder?.turnOwed ? holder.turnOpenedAt ?? ask.created_at : undefined;
}

export function orderAsks(asks: readonly Ask[], sessions: readonly AskHolderSession[], seeds: readonly Seed[]): Ask[] {
  return [...asks].sort((a, b) => {
    const aWaiting = waitingSince(a, sessions, seeds);
    const bWaiting = waitingSince(b, sessions, seeds);
    if (Boolean(aWaiting) !== Boolean(bWaiting)) return aWaiting ? -1 : 1;
    return (aWaiting ?? '').localeCompare(bWaiting ?? '') || a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id);
  });
}

export interface AskListItem<S extends AskHolderSession> {
  ask: Ask;
  holder: S | undefined;
  from: string;
  questionCount: number;
}

function askListItem<S extends AskHolderSession & { label: string }>(ask: Ask, sessions: readonly S[], seeds: readonly Seed[], crew: readonly Pick<CrewMember, 'key' | 'name'>[]): AskListItem<S> {
  const holder = askHolder(ask, sessions, seeds);
  return { ask, holder, from: askFrom(ask, holder, crew), questionCount: askQuestions(ask).length };
}

export function askList<S extends AskHolderSession & { label: string }>(asks: readonly Ask[], sessions: readonly S[], seeds: readonly Seed[], crew: readonly Pick<CrewMember, 'key' | 'name'>[]): AskListItem<S>[] {
  return orderAsks(asks, sessions, seeds).map((ask) => askListItem(ask, sessions, seeds, crew));
}

export function repliedAskList<S extends AskHolderSession & { label: string }>(asks: readonly Ask[], sessions: readonly S[], seeds: readonly Seed[], crew: readonly Pick<CrewMember, 'key' | 'name'>[]): AskListItem<S>[] {
  return [...asks]
    .sort((a, b) => (b.reply?.answered_at ?? '').localeCompare(a.reply?.answered_at ?? '') || a.id.localeCompare(b.id))
    .map((ask) => askListItem(ask, sessions, seeds, crew));
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
