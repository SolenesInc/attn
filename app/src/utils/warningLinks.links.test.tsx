import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { compactUrlLabel, renderWarningSegments, splitWarningLinks } from './warningLinks';

const LINUX_HINT = 'See https://github.com/cli/cli/blob/trunk/docs/install_linux.md';

describe('splitWarningLinks', () => {
  it('leaves plain text alone', () => {
    expect(splitWarningLinks('Run: brew upgrade gh')).toEqual([
      { key: 't0', text: 'Run: brew upgrade gh' },
    ]);
  });

  it('cuts the Linux doc URL out of the gh hints', () => {
    const segments = splitWarningLinks(`GitHub CLI not installed. PR monitoring disabled. ${LINUX_HINT}`);
    expect(segments).toEqual([
      { key: 't0', text: 'GitHub CLI not installed. PR monitoring disabled. See ' },
      {
        key: 'u1',
        url: 'https://github.com/cli/cli/blob/trunk/docs/install_linux.md',
        label: 'github.com/…',
      },
    ]);
  });

  it('keeps trailing sentence punctuation out of the URL', () => {
    const [text, link, comma, rest] = splitWarningLinks('See https://cli.github.com/install, then retry.');
    expect(text).toMatchObject({ text: 'See ' });
    expect(link).toMatchObject({ url: 'https://cli.github.com/install', label: 'cli.github.com/…' });
    expect(comma).toMatchObject({ text: ',' });
    expect(rest).toMatchObject({ text: ' then retry.' });
  });
});

describe('compactUrlLabel', () => {
  it('labels bare hosts without an ellipsis', () => {
    expect(compactUrlLabel('https://cli.github.com')).toBe('cli.github.com');
  });
});

describe('renderWarningSegments', () => {
  it('opens the full URL from the compact action', () => {
    const onOpenUrl = vi.fn();
    render(<span>{renderWarningSegments(`Outdated. ${LINUX_HINT}`, onOpenUrl)}</span>);
    const action = screen.getByRole('button', { name: 'github.com/…' });
    expect(action).toHaveAttribute(
      'title',
      'https://github.com/cli/cli/blob/trunk/docs/install_linux.md',
    );
    fireEvent.click(action);
    expect(onOpenUrl).toHaveBeenCalledWith(
      'https://github.com/cli/cli/blob/trunk/docs/install_linux.md',
    );
  });
});
