import { act, fireEvent, within } from '@testing-library/react';
import { codeToTokens } from 'shiki';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles } from './test/appFixtures';
import type { ReviewBundle, ReviewRow } from './types/generated';

const DOC = '/notes/review.md';

const DOCUMENT = `---
attn: review
frame: brief
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Number the functions

## Two functions

### big.go:10-12

A paragraph between the stops.

### big.go:130-132
`;

/** A Go file long enough that its tokens past line 100 take more than one
 * tokenize chunk, so a row there is colored at once only from kept tokens. */
function bigFile(renamed?: number): string[] {
  return ['package big', '', ...Array.from({ length: 148 }, (_, index) => `func f${index + 3}() int { return ${index === (renamed ?? -1) - 3 ? 'renamed' : index + 3} }`)];
}

const rowsOf = (lines: readonly string[]): ReviewRow[] => lines.map((text, index) => ({ kind: ' ', old: index + 1, now: index + 1, text }));

function bundleOf(rows: ReviewRow[], hash: string): ReviewBundle {
  const stop = (heading_line: number, from: number, to: number) => ({
    heading_line, path: 'big.go', from, to, now_from: from, now_to: to, changed_since_presented: false, file_hash: hash,
    rows: rows.slice(from - 1, to), notes: [],
  });
  return {
    frame: 'brief',
    repository: '/repo',
    base: '4be1c0d',
    head: '9f3a7e2',
    stops: [stop(13, 10, 12), stop(17, 130, 132)],
    files: [{ path: 'big.go', status: 'modified', added: 0, removed: 0, binary: false }, { path: 'other.go', status: 'modified', added: 0, removed: 0, binary: false }],
    flows: [],
    not_shown: [],
    navigation: [],
    problems: [],
  };
}

const CURSOR_ROW = '[data-review-file-view] .review-cursor';
const FILE_ROWS = '[data-review-file-view] .review-file-view-row';
const colored = (row: Element) => row.querySelector('.review-row-text span[style]') !== null;
const codeOf = (row: Element) => row.querySelector('.review-row-text')?.textContent ?? '';

/** Every state of the file view the DOM went through, recorded as each change
 * lands: the cursor row's code, whether it is colored, and the code of every
 * shown row that is not. */
function watchFileView(tile: HTMLElement) {
  const seen: Array<{ cursor: string; colored: boolean; plain: string[] }> = [];
  const record = () => {
    const row = tile.querySelector(CURSOR_ROW);
    if (!row) return;
    const at = { cursor: codeOf(row), colored: colored(row), plain: Array.from(tile.querySelectorAll(FILE_ROWS)).filter((shown) => !colored(shown) && codeOf(shown).trim() !== '').map(codeOf) };
    if (JSON.stringify(seen[seen.length - 1]) !== JSON.stringify(at)) seen.push(at);
  };
  const observer = new MutationObserver(record);
  observer.observe(tile, { subtree: true, childList: true, attributes: true, characterData: true });
  return {
    seen,
    cursor: () => seen.map(({ cursor, colored: isColored }) => ({ cursor, colored: isColored })).filter((at, index, all) => index === 0 || JSON.stringify(all[index - 1]) !== JSON.stringify(at)),
    reset: () => { seen.length = 0; },
    stop: () => observer.disconnect(),
  };
}

async function openReview() {
  const file = { lines: bigFile(), hash: 'b1' };
  const view = await openMarkdownTiles('', {
    path: DOC,
    script: (daemon) => {
      daemon.on('review_file', ({ path }) => (path === 'big.go'
        ? { event: 'review_file_result', success: true, path, rows: rowsOf(file.lines), hash: file.hash }
        : { event: 'review_file_result', success: true, path, rows: rowsOf(bigFile()), hash: 'o1' }));
    },
  });
  const show = async () => {
    view.daemon.emit({
      event: 'desktop_tile_content', desktop_id: view.daemon.arrangement.profile.current_desktop_id, tile_id: 'tile-a', tile_kind: 'markdown', path: DOC,
      content: DOCUMENT, review: bundleOf(rowsOf(file.lines), file.hash),
    });
    await view.daemon.idle();
  };
  await show();
  const tile = view.tile();
  tile.querySelector<HTMLElement>('.desktop-dock-tile-body')!.focus();
  const settle = async () => {
    await act(() => vi.advanceTimersByTimeAsync(0));
    await view.daemon.idle();
  };
  const press = async (key: string) => {
    fireEvent.keyDown(tile.ownerDocument.activeElement ?? tile, { key });
    await view.daemon.idle();
  };
  await settle();
  return { ...view, file, show, tile, press, settle };
}

const toLine = async (tile: HTMLElement, press: (key: string) => Promise<void>, line: string) => {
  for (let moves = 0; tile.querySelector('.review-doc .review-cursor')?.getAttribute('data-line') !== line; moves += 1) {
    if (moves > 20) throw new Error(`the cursor never reached line ${line}`);
    await press('ArrowDown');
  }
};

describe('App review tokens', () => {
  beforeAll(async () => {
    await codeToTokens('', { lang: 'go', themes: { light: 'github-light-default', dark: 'github-dark-default' } });
  });

  it('colors a stop file on its first open once the review has warmed it, and on every later open while its hash holds', async () => {
    const { tile, press, settle, daemon } = await openReview();
    expect(daemon.sentOf('review_file').map(({ path }) => path)).toEqual(['big.go']);

    await press('ArrowRight');
    await press('ArrowRight');
    await toLine(tile, press, '131');
    const watch = watchFileView(tile);
    await press('Enter');
    await settle();
    expect(watch.cursor()).toEqual([{ cursor: 'func f131() int { return 131 }', colored: true }]);

    await press('Escape');
    watch.reset();
    await press('Enter');
    await settle();
    expect(watch.cursor()).toEqual([{ cursor: 'func f131() int { return 131 }', colored: true }]);
    watch.stop();
  });

  it('colors a file reached by search once tokenized, then at once on the next open', async () => {
    const { tile, press, settle, daemon } = await openReview();
    daemon.on('review_search', () => ({ event: 'review_search_result', success: true, files: [], hits: [{ path: 'other.go', line: 120, text: 'func f120() int { return 120 }' }] }));
    const openOther = async () => {
      await press('/');
      fireEvent.change(within(tile).getByRole('textbox', { name: 'Search' }), { target: { value: 'f120' } });
      await settle();
      fireEvent.keyDown(within(tile).getByRole('textbox', { name: 'Search' }), { key: 'Enter' });
      await daemon.idle();
    };

    const watch = watchFileView(tile);
    await openOther();
    await settle();
    expect(watch.cursor()).toEqual([
      { cursor: 'func f120() int { return 120 }', colored: false },
      { cursor: 'func f120() int { return 120 }', colored: true },
    ]);

    await press('Escape');
    watch.reset();
    await openOther();
    await settle();
    expect(watch.cursor()).toEqual([{ cursor: 'func f120() int { return 120 }', colored: true }]);
    watch.stop();
  });

  it('keeps the colors of every unchanged line while a file with a changed hash is read again, and tokenizes it again', async () => {
    const { tile, press, settle, file, show } = await openReview();
    await press('ArrowRight');
    await press('ArrowRight');
    await toLine(tile, press, '131');
    await press('Enter');
    await settle();

    const watch = watchFileView(tile);
    file.lines = bigFile(110);
    file.hash = 'b2';
    await show();
    await settle();
    expect(watch.seen.some((at) => at.plain.includes('func f110() int { return renamed }'))).toBe(true);
    expect([...new Set(watch.seen.flatMap((at) => at.plain))]).toEqual(['func f110() int { return renamed }']);
    expect(watch.seen[watch.seen.length - 1]?.plain).toEqual([]);
    watch.stop();
  });

  it('never shows plain code while the Walkthrough steps between stops in one file', async () => {
    const { tile, press, settle } = await openReview();
    const watch = watchFileView(tile);
    await press('2');
    await settle();
    await press('ArrowRight');
    await settle();
    await press('ArrowLeft');
    await settle();
    expect(watch.cursor()).toEqual([
      { cursor: 'func f10() int { return 10 }', colored: true },
      { cursor: 'func f130() int { return 130 }', colored: true },
      { cursor: 'func f10() int { return 10 }', colored: true },
    ]);
    expect(watch.seen.flatMap((at) => at.plain)).toEqual([]);
    watch.stop();
  });
});
