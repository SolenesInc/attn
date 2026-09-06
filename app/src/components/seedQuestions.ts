import type { Seed } from '../hooks/useDaemonSocket';
import { crewDisplayName } from '../utils/crewName';

export const GARDEN_NEEDS_HUMAN_SETTING = 'garden_needs_human_enabled';

export function isGardenNeedsHumanEnabled(settings: Record<string, string>): boolean {
  return (settings[GARDEN_NEEDS_HUMAN_SETTING] || 'false') === 'true';
}

export type SeedQuestion = NonNullable<Seed['question']>;

export interface Asking {
  seed: Seed;
  question: SeedQuestion;
}

export function questionOf(seed: Seed | undefined): SeedQuestion | null {
  return seed?.question ?? null;
}

export function isOpenQuestion(question: SeedQuestion | null): boolean {
  return question?.status === 'open';
}

export function questionAuthor(question: SeedQuestion): string {
  if (question.asked_by_member) return crewDisplayName(question.asked_by_member);
  return question.asked_by_session ? `agent ${question.asked_by_session.slice(0, 8)}` : 'agent';
}

/** Longest wait first, with withdrawn tombstones after live questions. */
export function asking(seeds: Seed[]): Asking[] {
  const rows = seeds.flatMap((seed) => {
    const question = questionOf(seed);
    return question ? [{ seed, question }] : [];
  });
  return rows.sort((a, b) => {
    const liveOrder = Number(b.question.status === 'open') - Number(a.question.status === 'open');
    return liveOrder || Date.parse(a.question.asked_at) - Date.parse(b.question.asked_at);
  });
}

export function waitedWords(iso: string): string {
  const askedAt = Date.parse(iso);
  if (Number.isNaN(askedAt)) return '';
  const seconds = Math.max(0, Math.round((Date.now() - askedAt) / 1000));
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}
