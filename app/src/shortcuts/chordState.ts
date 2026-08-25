
import { Combo, ShortcutId, matchesShortcut } from './registry';

export const LEADER_TIMEOUT_MS = 600;

export interface ChordCandidate {
  id: ShortcutId;
  then: Combo;
}

interface PendingLeader {
  leader: Combo;
  candidates: ChordCandidate[];
  timer: ReturnType<typeof setTimeout>;
}

let pending: PendingLeader | null = null;

// Replaced only when the armed leader actually changes: a fresh object on every
// read would re-render every useSyncExternalStore subscriber.
let snapshot: { leader: Combo | null } = { leader: null };
const subscribers = new Set<() => void>();

function publish(leader: Combo | null): void {
  if (snapshot.leader === leader) return;
  snapshot = { leader };
  for (const cb of subscribers) cb();
}

export function subscribeChord(cb: () => void): () => void {
  subscribers.add(cb);
  return () => {
    subscribers.delete(cb);
  };
}

export function getChordSnapshot(): { leader: Combo | null } {
  return snapshot;
}

export function isLeaderPending(): boolean {
  return pending !== null;
}

export function enterLeader(leader: Combo, candidates: ChordCandidate[]): void {
  if (pending) clearTimeout(pending.timer);
  const timer = setTimeout(cancelLeader, LEADER_TIMEOUT_MS);
  pending = { leader, candidates, timer };
  publish(leader);
}

export function cancelLeader(): void {
  if (!pending) return;
  clearTimeout(pending.timer);
  pending = null;
  publish(null);
}

const MODIFIER_KEYS = new Set([
  'Meta', 'Control', 'Shift', 'Alt', 'AltGraph', 'OS', 'Hyper', 'Super',
  'CapsLock', 'Fn', 'FnLock', 'NumLock', 'ScrollLock', 'Dead',
]);

export type ThenResult =
  | { kind: 'none' }
  | { kind: 'fired'; id: ShortcutId }
  | { kind: 'rearmed' }
  | { kind: 'cancelled' };

// Every result except 'none' means the caller must consume the event so the key
// never reaches the PTY.
export function resolvePendingThen(e: KeyboardEvent): ThenResult {
  if (!pending) return { kind: 'none' };
  if (MODIFIER_KEYS.has(e.key)) return { kind: 'none' };
  if (e.key === 'Escape') {
    cancelLeader();
    return { kind: 'cancelled' };
  }
  for (const c of pending.candidates) {
    if (matchesShortcut(e, c.then)) {
      cancelLeader();
      return { kind: 'fired', id: c.id };
    }
  }
  if (e.repeat || matchesShortcut(e, pending.leader)) {
    enterLeader(pending.leader, pending.candidates);
    return { kind: 'rearmed' };
  }
  cancelLeader();
  return { kind: 'cancelled' };
}
