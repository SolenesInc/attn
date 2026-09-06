// PROTOTYPE (s-j3j5bk). The question rides on the seed as an untyped field;
// the real build puts it in the protocol. Real seeds carry none.
import type { Seed } from '../hooks/useDaemonSocket';

export interface SeedQuestion {
  id: string;
  seed_id: string;
  /** Crew member or session label of whoever raised it. */
  asked_by: string;
  asked_at: string;
  text: string;
  status: 'open' | 'withdrawn' | 'answered' | 'dismissed';
  /** Set once the question closes: the answer, or why it was withdrawn. */
  resolution?: string;
  resolved_at?: string;
}

export function questionOf(seed: Seed | undefined): SeedQuestion | null {
  const question = seed?.question as SeedQuestion | undefined;
  return question ?? null;
}

export function isOpen(question: SeedQuestion | null): boolean {
  return question?.status === 'open';
}

export interface Asking {
  seed: Seed;
  question: SeedQuestion;
}

/** Longest wait first: the seed that has been stuck the longest costs the most. */
export function asking(seeds: Seed[], statuses: SeedQuestion['status'][] = ['open']): Asking[] {
  const rows: Asking[] = [];
  for (const seed of seeds) {
    const question = questionOf(seed);
    if (question && statuses.includes(question.status)) rows.push({ seed, question });
  }
  const live = (row: Asking) => (row.question.status === 'open' ? 0 : 1);
  return rows.sort((a, b) =>
    live(a) - live(b) || Date.parse(a.question.asked_at) - Date.parse(b.question.asked_at));
}

export function agoWords(iso: string): string {
  const words = waitedWords(iso);
  return words === 'just now' ? words : `${words} ago`;
}

export function waitedWords(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const seconds = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}
