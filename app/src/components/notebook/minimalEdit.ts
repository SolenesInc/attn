// CodeMirror keeps scroll position and selection anchored across a minimal
// change, but snaps the viewport to the top when the whole document is replaced.

export interface MinimalEdit {
  // Offsets into the ORIGINAL string. Replace [from, to) with `insert` to get `next`.
  from: number;
  to: number;
  insert: string;
}

export function computeMinimalEdit(current: string, next: string): MinimalEdit | null {
  if (current === next) return null;

  const maxPrefix = Math.min(current.length, next.length);
  let prefix = 0;
  while (prefix < maxPrefix && current.charCodeAt(prefix) === next.charCodeAt(prefix)) {
    prefix++;
  }

  // Never back past the shared prefix on either side, so the range and the inserted
  // slice stay well-formed and non-overlapping.
  let endCur = current.length;
  let endNext = next.length;
  while (
    endCur > prefix &&
    endNext > prefix &&
    current.charCodeAt(endCur - 1) === next.charCodeAt(endNext - 1)
  ) {
    endCur--;
    endNext--;
  }

  return { from: prefix, to: endCur, insert: next.slice(prefix, endNext) };
}
