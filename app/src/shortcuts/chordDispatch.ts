import { Combo, matchesShortcut, isChord } from './registry';
import { resolvedShortcutEntries } from './resolver';
import { ChordCandidate } from './chordState';

export function matchChordLeader(
  e: KeyboardEvent,
): { leader: Combo; candidates: ChordCandidate[] } | null {
  let leader: Combo | null = null;
  const candidates: ChordCandidate[] = [];
  for (const [id, def] of resolvedShortcutEntries()) {
    if (isChord(def) && matchesShortcut(e, def.leader)) {
      leader = def.leader;
      candidates.push({ id, then: def.then });
    }
  }
  return leader ? { leader, candidates } : null;
}
