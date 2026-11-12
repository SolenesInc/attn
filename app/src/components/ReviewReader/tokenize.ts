import { useEffect, useState, useSyncExternalStore } from 'react';
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

export const THEMES = { light: 'github-light-default', dark: 'github-dark-default' } as const;

const NAMED_LANGUAGES: Record<string, string> = { makefile: 'make', dockerfile: 'docker' };

export type Shiki = NonNullable<Awaited<ReturnType<typeof loadShiki>>>;

export type Language = keyof Shiki['bundledLanguages'];

export function languageOf(shiki: Shiki, path: string): Language | undefined {
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

/** The most lines a reader keeps tokens for across the files it has opened.
 * Measured on the change of seed s-gt54ef: internal/daemon/daemon.go is 4,257
 * lines (19,575 tokens), and the five files its stops show are 7,476 lines
 * (33,877 tokens), about 7 MB in V8. */
export const TOKEN_CACHE_MAX_LINES = 25_000;

/** A file's tokens for one content hash as far as they are made, from line 1,
 * and the colors of its previous content by line text until they are whole. */
export interface FileTokens {
  readonly tokens: readonly (TokenLine | undefined)[];
  readonly earlier?: ReadonlyMap<string, TokenLine>;
}

const NO_TOKENS: FileTokens = { tokens: [] };

interface CachedFile {
  hash: string;
  lines: readonly string[];
  filled: (TokenLine | undefined)[];
  started: boolean;
  done: boolean;
  whole: Promise<void>;
  shown: FileTokens;
  asEarlier?: FileTokens;
}

function byText(file: CachedFile): Map<string, TokenLine> {
  const known = new Map(file.shown.earlier);
  file.filled.forEach((tokens, index) => { if (tokens) known.set(file.lines[index], tokens); });
  return known;
}

/** The tokens of the files one reader shows, one content hash per path. Each
 * (path, hash) is tokenized once; a full cache forgets the least recently
 * opened file. Files warmed ahead of their first open are tokenized one at a
 * time and count as opened before every file the user opened. */
export class TokenCache {
  private files = new Map<string, CachedFile>();
  private readonly listeners = new Set<() => void>();
  private warming = Promise.resolve();

  constructor(private readonly maxLines = TOKEN_CACHE_MAX_LINES) {}

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  /** What to draw for path at hash now: its tokens, or the colors of the
   * path's previous content while hash has none yet. */
  shown(path: string, hash: string | undefined): FileTokens {
    const file = this.files.get(path);
    if (!file || hash === undefined) return NO_TOKENS;
    if (file.hash === hash) return file.shown;
    file.asEarlier ??= { tokens: [], earlier: byText(file) };
    return file.asEarlier;
  }

  has(path: string, hash: string): boolean {
    return this.files.get(path)?.hash === hash;
  }

  /** Whether a file of this many lines fits beside the files kept and queued now. */
  fits(lines: number): boolean {
    let total = lines;
    for (const file of this.files.values()) total += file.lines.length;
    return total <= this.maxLines;
  }

  /** Queues path at hash to be tokenized after the files warmed before it. */
  warm(path: string, hash: string, lines: readonly string[]): Promise<void> {
    if (this.has(path, hash)) return this.files.get(path)!.whole;
    const file = this.add(path, hash, lines, false);
    file.whole = this.warming.then(() => this.start(path, file));
    this.warming = file.whole;
    return file.whole;
  }

  /** Tokenizes path at hash now unless it is kept, and marks it the most
   * recently opened. Resolves once its tokens are whole or it cannot be colored. */
  open(path: string, hash: string, lines: readonly string[]): Promise<void> {
    const kept = this.files.get(path);
    if (kept?.hash === hash) {
      this.files.delete(path);
      this.files.set(path, kept);
      return kept.started ? kept.whole : this.start(path, kept);
    }
    const file = this.add(path, hash, lines, true);
    file.whole = this.start(path, file);
    return file.whole;
  }

  private add(path: string, hash: string, lines: readonly string[], recent: boolean): CachedFile {
    const kept = this.files.get(path);
    this.files.delete(path);
    const file: CachedFile = { hash, lines, filled: Array.from({ length: lines.length }), started: false, done: false, whole: Promise.resolve(), shown: { tokens: [], earlier: kept ? byText(kept) : undefined } };
    if (recent) this.files.set(path, file);
    else this.files = new Map([[path, file], ...this.files]);
    return file;
  }

  private async start(path: string, file: CachedFile): Promise<void> {
    const current = () => this.files.get(path) === file;
    if (file.started || !current()) return;
    file.started = true;
    const colored = await tokenizeLines(file.lines, path, (start, chunk) => {
      chunk.forEach((line, index) => { file.filled[start + index] = line; });
      file.shown = { tokens: file.filled.slice(), earlier: file.shown.earlier };
      file.asEarlier = undefined;
      this.notify();
    }, () => !current());
    if (!current()) return;
    file.done = true;
    if (!colored) return;
    file.shown = { tokens: file.shown.tokens };
    this.notify();
    this.fit();
  }

  private fit() {
    let total = 0;
    for (const file of this.files.values()) total += file.lines.length;
    const newest = [...this.files.keys()].pop();
    for (const [path, file] of this.files) {
      if (total <= this.maxLines) return;
      if (!file.done || path === newest) continue;
      this.files.delete(path);
      total -= file.lines.length;
    }
  }

  private notify() {
    this.listeners.forEach((listener) => listener());
  }
}

/** The tokens to draw for a file's rows, from the reader's cache, which
 * tokenizes them when it does not keep them. */
export function useCachedTokens(cache: TokenCache, path: string, hash: string | undefined, lines: readonly string[] | null): FileTokens {
  useEffect(() => {
    if (hash !== undefined && lines && lines.length > 0) void cache.open(path, hash, lines);
  }, [cache, hash, lines, path]);
  return useSyncExternalStore(cache.subscribe, () => cache.shown(path, hash));
}
