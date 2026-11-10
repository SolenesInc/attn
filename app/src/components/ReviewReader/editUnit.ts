import type { CodeToTokensBaseOptions, GrammarState, ThemedToken } from 'shiki';
import { loadShiki } from '../Markdown/shiki';
import { languageOf, TOKENIZE_CHUNK_LINES, THEMES, type Language, type Shiki } from './tokenize';

export type EditUnitKind = 'selection' | 'string' | 'comment' | 'line';

/** Lines of a file, 0-based and inclusive, that one edit takes. */
export interface EditUnit {
  kind: EditUnitKind;
  from: number;
  to: number;
}

/** Lines on each side of the cursor first asked for explanations; the window
 * doubles while the unit reaches its edge. */
export const EXPLAIN_WINDOW_LINES = 40;

const THEME = THEMES.light;
const STRING_END = /punctuation\.definition\.string(\.[\w-]+)*\.end/;

interface StateCache {
  key: string;
  states: Array<GrammarState | undefined>;
}

let stateCache: StateCache | null = null;

type LastState = (code: string, options: CodeToTokensBaseOptions) => Promise<GrammarState>;

function lastState(shiki: Shiki, code: string, lang: Language, grammarState: GrammarState | undefined): Promise<GrammarState> {
  return (shiki.getLastGrammarState as LastState)(code, { lang, theme: THEME, grammarState });
}

/** The grammar state at the start of each chunk of lines, kept for the last file asked about. */
async function chunkStates(shiki: Shiki, lang: Language, lines: readonly string[], key: string, upTo: number): Promise<Array<GrammarState | undefined>> {
  if (stateCache?.key !== key) stateCache = { key, states: [undefined] };
  const { states } = stateCache;
  while (states.length * TOKENIZE_CHUNK_LINES <= upTo) {
    const start = (states.length - 1) * TOKENIZE_CHUNK_LINES;
    const code = lines.slice(start, start + TOKENIZE_CHUNK_LINES).join('\n');
    states.push(await lastState(shiki, code, lang, states[states.length - 1]));
  }
  return states;
}

async function stateAt(shiki: Shiki, lang: Language, lines: readonly string[], key: string, line: number): Promise<GrammarState | undefined> {
  const states = await chunkStates(shiki, lang, lines, key, line);
  const chunk = Math.floor(line / TOKENIZE_CHUNK_LINES);
  const start = chunk * TOKENIZE_CHUNK_LINES;
  if (start === line) return states[chunk];
  return lastState(shiki, lines.slice(start, line).join('\n'), lang, states[chunk]);
}

function scopesOf(token: ThemedToken): string[] {
  return token.explanation?.flatMap((part) => part.scopes.map((scope) => scope.scopeName)) ?? [];
}

type Span = 'string' | 'comment';

/** What a line leaves open at its end: a delimited string or a block comment
 * that goes on to the next line. An unquoted string, such as a YAML value or a
 * shell word, has no delimiter to close, so it never spans lines. */
function openAtEnd(tokens: readonly ThemedToken[]): Span | null {
  const last = [...tokens].reverse().find((token) => token.content.trim() !== '');
  if (!last) return null;
  const scopes = scopesOf(last);
  const strings = scopes.filter((scope) => scope.startsWith('string.'));
  if (strings.length > 0) {
    if (strings.every((scope) => scope.startsWith('string.unquoted'))) return null;
    return scopes.some((scope) => STRING_END.test(scope)) ? null : 'string';
  }
  if (scopes.some((scope) => scope.startsWith('comment.block')) && !last.content.trimEnd().endsWith('*/')) return 'comment';
  return null;
}

/** A line holding nothing but comment. */
function commentOnly(tokens: readonly ThemedToken[]): boolean {
  const words = tokens.filter((token) => token.content.trim() !== '');
  return words.length > 0 && words.every((token) => scopesOf(token).some((scope) => scope.startsWith('comment.')));
}

function spanAround(tokens: readonly ThemedToken[][], at: number): { kind: Span; from: number; to: number } | null {
  const open: Array<Span | null> = [];
  for (const [index, line] of tokens.entries()) {
    open.push(line.every((token) => token.content.trim() === '') && index > 0 ? open[index - 1] : openAtEnd(line));
  }
  let from = at;
  while (from > 0 && open[from - 1]) from -= 1;
  let to = at;
  while (to < tokens.length - 1 && open[to]) to += 1;
  if (to > from) return { kind: open[from] ?? 'string', from, to };
  if (!commentOnly(tokens[at])) return null;
  from = at;
  while (from > 0 && commentOnly(tokens[from - 1])) from -= 1;
  to = at;
  while (to < tokens.length - 1 && commentOnly(tokens[to + 1])) to += 1;
  return to > from ? { kind: 'comment', from, to } : null;
}

/**
 * What an edit at the cursor takes: the selected lines, else a whole string
 * or comment that spans several lines, else the cursor's line. The extent of
 * a string or comment comes from the language's grammar, asked for
 * explanations only around the cursor.
 */
export async function findEditUnit(lines: readonly string[], path: string, cursor: number, selection?: readonly [number, number]): Promise<EditUnit> {
  if (selection) {
    return { kind: 'selection', from: Math.min(...selection), to: Math.max(...selection) };
  }
  const line: EditUnit = { kind: 'line', from: cursor, to: cursor };
  const shiki = await loadShiki();
  const lang = shiki ? languageOf(shiki, path) : undefined;
  if (!shiki || !lang) return line;
  const key = `${path}\0${lines.join('\n')}`;
  for (let reach = EXPLAIN_WINDOW_LINES; ; reach *= 2) {
    const low = Math.max(0, cursor - reach);
    const high = Math.min(lines.length - 1, cursor + reach);
    const grammarState = await stateAt(shiki, lang, lines, key, low);
    const { tokens } = await shiki.codeToTokens(lines.slice(low, high + 1).join('\n'), {
      lang,
      theme: THEME,
      grammarState,
      includeExplanation: 'scopeName',
    });
    const span = spanAround(tokens, cursor - low);
    if (!span) return line;
    const atEdge = (span.from === 0 && low > 0) || (span.to === high - low && high < lines.length - 1);
    if (!atEdge) return { kind: span.kind, from: span.from + low, to: span.to + low };
  }
}
