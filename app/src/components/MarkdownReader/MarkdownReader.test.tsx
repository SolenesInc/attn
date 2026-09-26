import { describe, expect, it, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import { MarkdownReader } from './index';
import { sanitizeLinkUrl } from './markdownLinks';
import { fileMarkdownSource } from './documentSource';

const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    `<span style="--shiki-light:#000;--shiki-dark:#fff">${code}</span>`),
}));
vi.mock('shiki', () => shikiMock);

const FILE_SOURCE = fileMarkdownSource('workspace-1', '/tmp/project/README.md');

type Facts = Record<string, string | boolean | null>;

interface Rendering {
  rule: string;
  markdown: string;
  elements?: Record<string, Facts[]>;
  shows?: string[];
  never?: string[];
}

function fact(element: Element, key: string): string | boolean | null {
  switch (key) {
    case 'text': return element.textContent!.replace(/\s+/g, ' ').trim();
    case 'id': return element.id;
    case 'checked': return (element as HTMLInputElement).checked;
    case 'disabled': return (element as HTMLInputElement).disabled;
    case 'open': return (element as HTMLDetailsElement).open;
    case 'textAlign': return (element as HTMLElement).style.textAlign;
    default: return element.getAttribute(key);
  }
}

const ALERTS = [
  ['NOTE', 'note', 'Note'],
  ['TIP', 'tip', 'Tip'],
  ['WARNING', 'warning', 'Warning'],
  ['CAUTION', 'caution', 'Caution'],
  ['IMPORTANT', 'important', 'Important'],
] as const;

const RENDERINGS: Rendering[] = [
  {
    rule: 'stamps source lines and block ids on rendered blocks',
    markdown: '# Title\n\nFirst paragraph.\n\n- item one\n- item two\n',
    elements: {
      h1: [{ 'data-source-line': '1', 'data-block-id': 'b0-heading' }],
      p: [{ 'data-source-line': '3', 'data-source-line-end': '3' }],
      'li[data-source-line]': [{ 'data-source-line': '5' }, { 'data-source-line': '6' }],
    },
  },
  {
    rule: 'keeps raw-file line numbers for blocks after frontmatter',
    markdown: '---\ntitle: Plan\ntags: [a, b]\n---\n\nBody paragraph.\n',
    elements: { p: [{ 'data-source-line': '6', 'data-source-line-end': '6' }] },
  },
  {
    rule: 'stamps a fenced code block across its full fence range',
    markdown: 'intro\n\n```js\nconst x = 1;\n```\n',
    elements: { pre: [{ 'data-source-line': '3', 'data-source-line-end': '5' }] },
  },
  {
    rule: 'shows frontmatter as scalar rows and tag chips, never as prose',
    markdown: '---\ntitle: My Plan\ntags: [alpha, beta]\n---\n\nBody.\n',
    elements: { '.md-frontmatter': [{}], h2: [] },
    shows: ['title:', 'My Plan', 'alpha', 'beta'],
    never: ['---'],
  },
  {
    rule: 'renders dangerous links as plain text',
    markdown: '[boom](javascript:alert(1)) and [leak](data:text/html,x)',
    elements: { a: [] },
    shows: ['boom', 'leak'],
  },
  {
    rule: 'gives headings GitHub slugs, deduplicated',
    markdown: '## Configuração!\n\n## Configuração?\n',
    elements: { h2: [{ id: 'configuração' }, { id: 'configuração-1' }] },
  },
  {
    rule: 'slugs a heading from its text before emoji shortcodes turn into emoji',
    markdown: '## Deploy :rocket:\n',
    elements: { h2: [{ id: 'deploy-rocket', text: 'Deploy 🚀' }] },
  },
  ...ALERTS.map(([marker, kind, title]) => ({
    rule: `renders [!${marker}] as an alert with its icon and title`,
    markdown: `> [!${marker}]\n> Alert body text.\n`,
    elements: {
      [`.md-alert-${kind}`]: [{ 'data-alert-kind': kind, text: `${title} Alert body text.` }],
      '.md-alert-title svg': [{}],
      blockquote: [],
    },
  })),
  {
    rule: 'reads an alert marker case-insensitively and anchors the alert like the blockquote it replaces',
    markdown: 'intro\n\n> [!note]\n> Body.\n',
    elements: { '.md-alert-note': [{ 'data-block-id': 'b1-blockquote', 'data-source-line': '3', 'data-source-line-end': '4' }] },
  },
  {
    rule: 'keeps list content inside an alert',
    markdown: '> [!TIP]\n> - item one\n> - item two\n',
    elements: { '.md-alert-tip li': [{ text: 'item one' }, { text: 'item two' }] },
  },
  {
    rule: 'leaves blockquotes that are not alerts as blockquotes',
    markdown: '> Just a quote.\n\n> [!NOTE] trailing words disqualify\n',
    elements: { blockquote: [{ text: 'Just a quote.' }, { text: '[!NOTE] trailing words disqualify' }], '.md-alert': [] },
  },
  {
    rule: 'renders task list items as read-only checkboxes, anchored',
    markdown: '- [x] done thing\n- [ ] open thing\n',
    elements: {
      'input[type="checkbox"]': [{ checked: true, disabled: true }, { checked: false, disabled: true }],
      'li.task-list-item[data-source-line]': [{ text: 'done thing' }, { text: 'open thing' }],
    },
  },
  {
    rule: 'keeps GFM column alignment',
    markdown: '| L | R |\n| :-- | --: |\n| a | b |\n',
    elements: { td: [{ textAlign: 'left' }, { textAlign: 'right' }] },
  },
  {
    rule: 'strips scripts, styles and event handlers from raw HTML, keeping allowed elements',
    markdown: 'before\n\n<script>window.pwned = true;</script>\n\n<style>body { display: none; }</style>\n\n<div onclick="window.pwned = true" title="ok">clickable</div>\n\nUse <kbd>Cmd</kbd>+<kbd>C</kbd>, H<sub>2</sub>O and x<sup>2</sup>.<br>done\n',
    elements: {
      script: [],
      style: [],
      'div[title="ok"]': [{ onclick: null, text: 'clickable' }],
      kbd: [{ text: 'Cmd' }, { text: 'C' }],
      sub: [{ text: '2' }],
      sup: [{ text: '2' }],
      'p br': [{}],
    },
    never: ['pwned', 'display: none'],
  },
  {
    rule: 'keeps <details> with its open state and anchors it like any block',
    markdown: '<details open>\n<summary>More</summary>\n\nHidden **body** text.\n\n</details>\n',
    elements: {
      details: [{ open: true, 'data-source-line': '1', 'data-block-id': 'b0-details' }],
      'details summary': [{ text: 'More' }],
      'details strong': [{ text: 'body' }],
    },
  },
  {
    rule: 'never lets raw HTML reach the network',
    markdown: '<img src="docs/pic.png" srcset="https://evil.example/pixel.png 1x">\n\n'
      + '<video src="https://evil.example/v.mp4" poster="https://evil.example/p.png" controls></video>\n\n'
      + '<picture><source srcset="https://evil.example/s.png"><img src="docs/pic.png"></picture>\n',
    elements: {
      video: [],
      source: [],
      img: [
        { srcset: null, src: 'asset://localhost//tmp/project/docs/pic.png' },
        { srcset: null, src: 'asset://localhost//tmp/project/docs/pic.png' },
      ],
    },
    never: ['evil.example'],
  },
  {
    rule: 'never takes anchoring attributes from author HTML',
    markdown: '<p data-block-id="b999-fake" data-source-line="999">spoof</p>\n',
    elements: { p: [{ 'data-block-id': 'b0-paragraph', 'data-source-line': '1', text: 'spoof' }] },
  },
  {
    rule: 'applies smart punctuation and emoji to prose but never to code or flags',
    markdown: 'He said "hello" -- ranges 3--5 work... :rocket:\n\nRun `bun --watch` with --verbose\n\n```sh\necho "raw" 3--5\n```\n',
    elements: { ':not(pre) > code': [{ text: 'bun --watch' }], 'pre code': [{ text: 'echo "raw" 3--5' }] },
    shows: ['“hello”', '3–5', '…', '🚀', '--verbose'],
  },
  {
    rule: 'transforms link labels but not hrefs',
    markdown: '["quoted label"](https://example.test/a--b)\n',
    elements: { a: [{ text: '“quoted label”', href: 'https://example.test/a--b' }] },
  },
];

describe('MarkdownReader rendering', () => {
  it.each(RENDERINGS)('$rule', async ({ markdown, elements = {}, shows = [], never = [] }) => {
    const { container } = render(<MarkdownReader content={markdown} source={FILE_SOURCE} allowLocalTargets />);
    await act(async () => {});

    for (const [selector, expected] of Object.entries(elements)) {
      const found = Array.from(container.querySelectorAll(selector));
      const facts = found.map((element, index) => Object.fromEntries(Object.keys(expected[index] ?? {}).map((key) => [key, fact(element, key)])));
      expect({ selector, facts }).toEqual({ selector, facts: expected });
    }
    for (const text of shows) expect(container.textContent).toContain(text);
    for (const text of never) expect(container.innerHTML).not.toContain(text);
  });
});

describe('sanitizeLinkUrl', () => {
  it.each([
    ['javascript:alert(1)', null],
    [' JavaScript:alert(1)', null],
    ['data:text/html,<script>', null],
    ['vbscript:msgbox', null],
    ['https://example.test/x', 'https://example.test/x'],
    ['docs/setup.md', 'docs/setup.md'],
    ['#fragment', '#fragment'],
  ])('turns %j into %j', (url, sanitized) => {
    expect(sanitizeLinkUrl(url)).toBe(sanitized);
  });
});
