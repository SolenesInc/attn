import { describe, expect, it, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import { Markdown } from './index';

const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    `<span style="--shiki-light:#000;--shiki-dark:#fff">${code}</span>`),
}));
vi.mock('shiki', () => shikiMock);

// Mermaid is here only to prove it is NOT sent to shiki; rendering one for real
// leaves an async tail that outlives the test, so it is stubbed.
const mermaidMock = vi.hoisted(() => ({
  initialize: vi.fn(),
  render: vi.fn(async () => ({ svg: '<svg data-stub="diagram"></svg>' })),
}));
vi.mock('mermaid', () => ({ default: mermaidMock }));

const FENCE = '```ts\nconst answer = 42;\n```\n';

describe('code highlighting', () => {
  it('highlights a settled code block', async () => {
    const { container } = render(<Markdown>{FENCE}</Markdown>);
    await waitFor(() => {
      expect(container.querySelector('code.markdown-shiki')).not.toBeNull();
    });
    expect(shikiMock.codeToHtml).toHaveBeenCalledWith(
      'const answer = 42;\n',
      expect.objectContaining({ lang: 'ts' }),
    );
  });

  it('leaves the open tail of a streaming message alone', async () => {
    shikiMock.codeToHtml.mockClear();
    const { container } = render(<Markdown streaming>{'intro\n\n```ts\nconst answer = 42;'}</Markdown>);
    await waitFor(() => {
      expect(container.querySelector('pre')).not.toBeNull();
    });
    expect(container.querySelector('code.markdown-shiki')).toBeNull();
    expect(shikiMock.codeToHtml).not.toHaveBeenCalled();
  });

  it('highlights the settled half while the tail of the same message waits', async () => {
    shikiMock.codeToHtml.mockClear();
    const streamed = `${FENCE}\nand then a second block:\n\n\`\`\`ts\nconst pending =`;
    const { container } = render(<Markdown streaming>{streamed}</Markdown>);
    await waitFor(() => {
      expect(container.querySelectorAll('code.markdown-shiki')).toHaveLength(1);
    });
    expect(shikiMock.codeToHtml).toHaveBeenCalledTimes(1);
    expect(shikiMock.codeToHtml).toHaveBeenCalledWith(
      'const answer = 42;\n',
      expect.objectContaining({ lang: 'ts' }),
    );
  });

  it('never asks shiki to highlight a diagram', async () => {
    shikiMock.codeToHtml.mockClear();
    render(<Markdown>{'```mermaid\nflowchart LR\n  A --> B\n```\n'}</Markdown>);
    await waitFor(() => {
      expect(shikiMock.codeToHtml).not.toHaveBeenCalled();
    });
  });

  it('leaves inline code unhighlighted', async () => {
    shikiMock.codeToHtml.mockClear();
    const { container } = render(<Markdown>{'a paragraph with `inline` code\n'}</Markdown>);
    await waitFor(() => {
      expect(container.querySelector('code')).not.toBeNull();
    });
    expect(container.querySelector('code.markdown-shiki')).toBeNull();
    expect(shikiMock.codeToHtml).not.toHaveBeenCalled();
  });
});
