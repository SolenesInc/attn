import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, render } from '@testing-library/react';
import { Markdown } from './index';

// Never settles on purpose: a resolving mock measures highlighting too, while
// this test isolates the parse split.
const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(() => new Promise<string>(() => {})),
}));
vi.mock('shiki', () => shikiMock);

function prefixes(): string[] {
  const settledPrefix = Array.from({ length: 32 }, (_, index) => `
## Streaming section ${index + 1}

This paragraph has **bold text**, a [link](https://example.com/${index}), and enough prose to exercise inline parsing while the final section changes.

| item | value |
| --- | --- |
| alpha | ${index} |
| beta | \`value-${index}\` |

\`\`\`ts
export const section${index} = ${index};
\`\`\`
`).join('\n');
  const tailDeltas = Array.from({ length: 48 }, (_, index) =>
    `Live detail ${index + 1} adds ordinary prose to the open response. `);

  return tailDeltas.map((_, index) =>
    `${settledPrefix}\n## Live tail\n\n${tailDeltas.slice(0, index + 1).join('')}`);
}

function replay(streaming: boolean, texts: string[]): number[] {
  const { rerender } = render(<Markdown streaming={streaming}>{texts[0]}</Markdown>);
  const samples: number[] = [];
  for (const text of texts.slice(1)) {
    const started = performance.now();
    act(() => { rerender(<Markdown streaming={streaming}>{text}</Markdown>); });
    samples.push(performance.now() - started);
  }
  return samples.sort((a, b) => a - b);
}

describe('streaming markdown cost', () => {
  afterEach(cleanup);

  it('reparses only the open tail', { timeout: 300_000 }, () => {
    const texts = prefixes();
    const quantile = (s: number[], p: number) => s[Math.min(s.length - 1, Math.floor(s.length * p))];
    const report = (label: string, s: number[]) =>
      `${label} p50 ${quantile(s, 0.5).toFixed(2)}ms p90 ${quantile(s, 0.9).toFixed(2)}ms p99 ${quantile(s, 0.99).toFixed(2)}ms max ${quantile(s, 1).toFixed(2)}ms`;

    const naive = replay(false, texts);
    cleanup();
    const split = replay(true, texts);
    console.log(`[streaming markdown ${texts.length} deltas → ${texts[texts.length - 1].length} chars]`);
    console.log(`  ${report('whole-document reparse:', naive)}`);
    console.log(`  ${report('settled/tail split:    ', split)}`);
    console.log(`  p50 ratio ${(quantile(naive, 0.5) / quantile(split, 0.5)).toFixed(1)}x`);

    expect(quantile(split, 0.5)).toBeLessThan(quantile(naive, 0.5));
  });
});
