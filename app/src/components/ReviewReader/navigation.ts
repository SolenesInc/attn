import type { CodeNavLocation } from '../../types/generated';

export type NavKind = 'definition' | 'references' | 'hover';

export interface LabeledName {
  name: string;
  /** UTF-16 offset of the name in its line, as the language server counts. */
  character: number;
  label: string;
}

/** A line the reader can ask about: a row of code at head. */
export interface NavLine {
  path: string;
  line: number;
  text: string;
  /** Where the line's labels render: a document row's cursor key, or a file view row. */
  at: { cursorKey: string } | { fileId: number; row: number };
}

export interface Picking {
  kind: NavKind;
  line: NavLine;
  names: LabeledName[];
  typed: string;
}

export interface NavAnswer {
  kind: NavKind;
  name: string;
  locations: CodeNavLocation[];
  text?: string;
  /** The answer arrived after the user moved on, so it waits to be opened instead of moving the view. */
  offered?: boolean;
  cursor: number;
}

/** An answer slower than this, after the cursor moved, is offered instead of followed. The prototype
 * showed a jump that lands after the user moved on loses their place (F10); 1.5 s is the plot's line. */
export const LATE_ANSWER_MS = 1500;

const NAME = /[\p{L}_][\p{L}\p{N}_]*/gu;

export function namesOn(text: string): Array<{ name: string; character: number }> {
  return Array.from(text.matchAll(NAME), (match) => ({ name: match[0], character: match.index ?? 0 }));
}

const LETTERS = 'asdfghjklqwertyuiopzxcvbnm';

/** One letter a name while the line has no more names than letters, then two. */
export function labelNames(names: ReadonlyArray<{ name: string; character: number }>): LabeledName[] {
  const width = names.length <= LETTERS.length ? 1 : 2;
  return names.map((name, index) => ({
    ...name,
    label: width === 1 ? LETTERS[index] : LETTERS[Math.floor(index / LETTERS.length)] + LETTERS[index % LETTERS.length],
  }));
}

export function isOwnDefinition(location: CodeNavLocation, path: string, line: number, character: number): boolean {
  return location.path === path && location.line === line && location.end_line === line
    && character >= location.character && character <= location.end_character;
}

export function navTitle(kind: NavKind, name: string): string {
  switch (kind) {
    case 'definition': return `Definition of ${name}`;
    case 'references': return `Uses of ${name}`;
    case 'hover': return `Type of ${name}`;
  }
}
