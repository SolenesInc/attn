// Pins the headless pipeline to the live one: for every [data-block-id] element the
// chrome-skipped DOM walk text equals extractBlockTexts' text for that id.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import { MarkdownReader } from '../index';
import { fileMarkdownSource } from '../documentSource';
import { blockDomText, resolveDomRange } from './domRange';
import { extractBlockTexts } from './extractBlocks';
import { reviewChromeOf } from '../../ReviewReader/reviewChrome';
import type { ReviewBundle } from '../../../types/generated';

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  },
}));

// Mirrors REAL shiki `structure: 'inline'` output: per-line spans joined by <br> ELEMENTS, no '\n' text nodes.
const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    code
      .split('\n')
      .map((line) => `<span style="--shiki-light:#000;--shiki-dark:#fff">${line}</span>`)
      .join('<br>')),
  codeToTokens: vi.fn(async (code: string) => ({ tokens: code.split('\n').map((line) => [{ content: line }]) })),
  bundledLanguages: { go: {} },
}));
vi.mock('shiki', () => shikiMock);

function renderReader(content: string, review?: ReviewBundle) {
  return render(
    <MarkdownReader
      content={content}
      source={fileMarkdownSource('/tmp/project/README.md')}
      allowLocalTargets={true}
      review={review}
    />,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

const KITCHEN_SINK = `---
title: Parity
---

# Deploy :rocket:

A paragraph with "smart quotes", an em---dash, \`inline code\`, **bold**,
and a [link](https://example.com).

> [!NOTE]
> Alert body text stays anchored.

> A plain blockquote.

- item one
- item two has 👍👍 emoji
  - nested item
- [ ] task item

1. first
2. second

| Col A | Col B |
| ----- | ----- |
| a1    | b1    |

\`\`\`js
const x = 1;
\`\`\`

\`\`\`mermaid
graph TD; A-->B;
\`\`\`

![remote](https://example.com/pic.png)

Last paragraph 3--5 range...
`;

describe('pipeline parity (headless extraction vs live DOM)', () => {
  it('chrome-skipped DOM text equals extractBlockTexts for every stamped block', () => {
    const { container } = renderReader(KITCHEN_SINK);
    const blocks = extractBlockTexts(KITCHEN_SINK);
    expect(blocks.length).toBeGreaterThan(10);

    const domBlocks = [...container.querySelectorAll('[data-block-id]')];
    expect(domBlocks.map((el) => el.getAttribute('data-block-id'))).toEqual(
      blocks.map((b) => b.blockId),
    );

    for (const block of blocks) {
      if (block.nonPaintable) {
        continue; // mermaid: code text becomes an svg diagram (documented divergence)
      }
      const el = container.querySelector(`[data-block-id="${block.blockId}"]`)!;
      expect(el, block.blockId).not.toBeNull();
      expect(blockDomText(el), block.blockId).toBe(block.text);
    }
  });

  it('keeps parity and offsets for a multi-line shiki block AFTER async hydration', async () => {
    const doc = 'Intro.\n\n```js\nconst a = 1;\nconst b = 2;\nconst c = 3;\n```\n';
    const { container } = renderReader(doc);
    await waitFor(() => expect(container.querySelector('.md-shiki')).not.toBeNull());

    const block = extractBlockTexts(doc).find((b) => b.text.includes('const b'))!;
    const el = container.querySelector(`[data-block-id="${block.blockId}"]`)!;
    expect(blockDomText(el)).toBe(block.text);

    const start = block.text.indexOf('const c = 3;');
    const range = resolveDomRange(el, start, start + 'const c = 3;'.length);
    expect(range?.toString()).toBe('const c = 3;');
  });

  it('keeps parity and the prose offsets around review stops, which render as chrome', () => {
    const review = '---\nattn: review\nrepository: /repo\nhead: abc\n---\n\n# Title\n\nBefore the stop.\n\n### calc.go:1-2\n\n- L1: A note.\n- L2!: Look.\n\nAfter the stop.\n\n## Not shown\n\n- go.sum: checksums.\n';
    const bundle: ReviewBundle = {
      frame: 'brief', repository: '/repo', head: 'abc', files: [], flows: [], navigation: [], problems: [],
      stops: [{ heading_line: 11, path: 'calc.go', from: 1, to: 2, now_from: 1, now_to: 2, changed_since_presented: false, file_hash: 'h',
        rows: [{ kind: ' ', old: 1, now: 1, text: 'package calc' }],
        notes: [{ line: 13, from: 1, to: 1, now_from: 1, now_to: 1, look: false, text: 'A note.' }, { line: 14, from: 2, to: 2, now_from: 2, now_to: 2, look: true, text: 'Look.' }] }],
      not_shown: [{ line: 20, path: 'go.sum', reason: 'checksums.' }],
    };
    const { container } = renderReader(review, bundle);
    const blocks = extractBlockTexts(review, reviewChromeOf(bundle));
    const plain = extractBlockTexts(review);

    const domBlocks = [...container.querySelectorAll('[data-block-id]')];
    expect(domBlocks.map((el) => el.getAttribute('data-block-id'))).toEqual(blocks.map((b) => b.blockId));
    for (const block of blocks) {
      expect(blockDomText(container.querySelector(`[data-block-id="${block.blockId}"]`)!), block.blockId).toBe(block.text);
    }
    const after = (list: typeof blocks) => list.find((b) => b.text === 'After the stop.');
    expect(after(blocks)?.blockId).toBe(after(plain)?.blockId);
    expect(blocks.map((b) => b.text)).not.toContain('calc.go:1-2');
    expect(container.querySelector('.review-stop')).toHaveTextContent('package calc');
  });
});
