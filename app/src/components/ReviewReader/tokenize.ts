import { useEffect, useState } from 'react';
import { loadShiki } from '../Markdown/shiki';

export interface CodeToken {
  content: string;
  style?: Record<string, string>;
}

export type TokenLine = CodeToken[];

/** Lines per tokenize call. Tokenizing internal/daemon/daemon.go (4,295
 * lines) in 100-line chunks took at most 9 ms a chunk in Node, under one
 * frame; the next chunk waits for the next task. */
export const TOKENIZE_CHUNK_LINES = 100;

const THEMES = { light: 'github-light-default', dark: 'github-dark-default' } as const;

const NAMED_LANGUAGES: Record<string, string> = { makefile: 'make', dockerfile: 'docker' };

type Shiki = NonNullable<Awaited<ReturnType<typeof loadShiki>>>;

type Language = keyof Shiki['bundledLanguages'];

function languageOf(shiki: Shiki, path: string): Language | undefined {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  const extension = name.includes('.') ? name.slice(name.lastIndexOf('.') + 1) : '';
  const candidate = NAMED_LANGUAGES[name] ?? extension;
  return candidate in shiki.bundledLanguages ? candidate as Language : undefined;
}

function nextTask(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

/** Tokenizes lines in chunks, each chunk continuing the grammar state of the
 * last, and hands each chunk over as it is done. Resolves false when the
 * file has no known language or the highlighter is unavailable. */
export async function tokenizeLines(
  lines: readonly string[],
  path: string,
  onChunk: (start: number, tokens: TokenLine[]) => void,
  isCancelled: () => boolean,
): Promise<boolean> {
  const shiki = await loadShiki();
  if (!shiki || isCancelled()) return false;
  const lang = languageOf(shiki, path);
  if (!lang) return false;
  let grammarState: Parameters<typeof shiki.codeToTokens>[1]['grammarState'];
  for (let start = 0; start < lines.length; start += TOKENIZE_CHUNK_LINES) {
    const result = await shiki.codeToTokens(lines.slice(start, start + TOKENIZE_CHUNK_LINES).join('\n'), {
      lang,
      themes: THEMES,
      defaultColor: false,
      grammarState,
    });
    if (isCancelled()) return false;
    grammarState = result.grammarState;
    onChunk(start, result.tokens.map((line) => line.map((token) => ({ content: token.content, style: token.htmlStyle as Record<string, string> | undefined }))));
    await nextTask();
    if (isCancelled()) return false;
  }
  return true;
}

/** The tokens of a short run of lines, such as a stop; null until they are
 * ready or when the file has no known language. */
export function useTokens(lines: readonly string[], path: string): TokenLine[] | null {
  const key = `${path}\0${lines.join('\n')}`;
  const [state, setState] = useState<{ key: string; tokens: TokenLine[] } | null>(null);
  useEffect(() => {
    let cancelled = false;
    const tokens: TokenLine[] = [];
    void tokenizeLines(lines, path, (start, chunk) => {
      tokens.splice(start, chunk.length, ...chunk);
    }, () => cancelled).then((done) => {
      if (done && !cancelled) setState({ key, tokens });
    });
    return () => { cancelled = true; };
  }, [key]);
  return state?.key === key ? state.tokens : null;
}
