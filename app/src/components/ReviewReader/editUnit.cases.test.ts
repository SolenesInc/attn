import { describe, expect, it } from 'vitest';
import { EXPLAIN_WINDOW_LINES, findEditUnit, type EditUnit } from './editUnit';

const goFile = [
  'package store',
  '',
  'const listPRs = `',
  'SELECT id, title',
  '',
  'FROM prs',
  'WHERE host = ?`',
  '',
  '// ReplacePRs swaps the hosts\' pull requests',
  '// and says which changed.',
  'func ReplacePRs() {',
  '\t/* the two reads',
  '\t   were one */',
  '\tx := "one line" // trailing',
  '}',
];

const tsFile = [
  'const query = `',
  '  select ${table}',
  '  where id = 1`;',
  '/**',
  ' * Documented.',
  ' */',
  'export const n = 1;',
];

const workflow = [
  'name: ci',
  'on: push',
  'jobs:',
  '  test:',
  '    runs-on: ubuntu-latest',
  '    steps:',
  '      - run: |',
  '          go test ./...',
  '          echo done',
];

const script = ['#!/bin/sh', 'echo hello world', 'cd "$dir"', 'printf "%s\\n" one'];

const settings = Array.from({ length: 4000 }, (_, index) => `key${index}: value ${index}`);

const filler = (count: number) => Array.from({ length: count }, (_, index) => `var v${index} = ${index}`);
const longString = [...filler(250), 'const long = `', ...Array.from({ length: 3 * EXPLAIN_WINDOW_LINES }, (_, index) => `line ${index}`), '`', 'var after = 1'];
const longStringStart = 250;
const longStringEnd = 250 + 3 * EXPLAIN_WINDOW_LINES + 1;

interface Case {
  name: string;
  lines: readonly string[];
  path: string;
  cursor: number;
  selection?: [number, number];
  want: EditUnit;
}

const cases: Case[] = [
  { name: 'a raw string of several lines in Go, from inside it', lines: goFile, path: 'store.go', cursor: 3, want: { kind: 'string', from: 2, to: 6 } },
  { name: 'a raw string in Go, from the blank line inside it', lines: goFile, path: 'store.go', cursor: 4, want: { kind: 'string', from: 2, to: 6 } },
  { name: 'a raw string in Go, from the line that opens it', lines: goFile, path: 'store.go', cursor: 2, want: { kind: 'string', from: 2, to: 6 } },
  { name: 'line comments of several lines in Go', lines: goFile, path: 'store.go', cursor: 9, want: { kind: 'comment', from: 8, to: 9 } },
  { name: 'a block comment of several lines in Go', lines: goFile, path: 'store.go', cursor: 12, want: { kind: 'comment', from: 11, to: 12 } },
  { name: 'a line of code with a one-line string and a trailing comment', lines: goFile, path: 'store.go', cursor: 13, want: { kind: 'line', from: 13, to: 13 } },
  { name: 'a single line of code', lines: goFile, path: 'store.go', cursor: 10, want: { kind: 'line', from: 10, to: 10 } },
  { name: 'a template literal in TypeScript', lines: tsFile, path: 'query.ts', cursor: 1, want: { kind: 'string', from: 0, to: 2 } },
  { name: 'a doc comment in TypeScript', lines: tsFile, path: 'query.ts', cursor: 4, want: { kind: 'comment', from: 3, to: 5 } },
  { name: 'a plain YAML value', lines: workflow, path: 'ci.yml', cursor: 4, want: { kind: 'line', from: 4, to: 4 } },
  { name: 'a YAML key and value in a long file of them', lines: settings, path: 'settings.yaml', cursor: 2000, want: { kind: 'line', from: 2000, to: 2000 } },
  { name: 'shell words', lines: script, path: 'run.sh', cursor: 1, want: { kind: 'line', from: 1, to: 1 } },
  { name: 'a one-line selection', lines: goFile, path: 'store.go', cursor: 3, selection: [3, 3], want: { kind: 'selection', from: 3, to: 3 } },
  { name: 'selected lines', lines: goFile, path: 'store.go', cursor: 10, selection: [13, 10], want: { kind: 'selection', from: 10, to: 13 } },
  { name: 'a language Shiki does not know', lines: ['`', 'a', '`'], path: 'notes.unknownlang', cursor: 1, want: { kind: 'line', from: 1, to: 1 } },
  {
    name: 'a string longer than the first window, far into the file',
    lines: longString,
    path: 'long.go',
    cursor: longStringStart + 2 * EXPLAIN_WINDOW_LINES,
    want: { kind: 'string', from: longStringStart, to: longStringEnd },
  },
];

describe('the edit unit at the cursor', () => {
  it.each(cases)('$name', async ({ lines, path, cursor, selection, want }) => {
    expect(await findEditUnit(lines, path, cursor, selection)).toEqual(want);
  });
});
