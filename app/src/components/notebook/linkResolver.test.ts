import { describe, expect, it } from 'vitest';
import { headingSlug, noteDir, resolveNotebookLink, type ResolvedLink } from './linkResolver';

describe('resolveNotebookLink', () => {
  it.each<[string, string, ResolvedLink]>([
    ['foo.md', 'knowledge/areas', { kind: 'note', path: 'knowledge/areas/foo.md', anchor: undefined }],
    ['foo.md', noteDir('knowledge/areas/note.md'), { kind: 'note', path: 'knowledge/areas/foo.md', anchor: undefined }],
    ['foo.md', noteDir('note.md'), { kind: 'note', path: 'foo.md', anchor: undefined }],
    ['./foo.md', 'knowledge', { kind: 'note', path: 'knowledge/foo.md', anchor: undefined }],
    ['../bar.md', 'knowledge/areas', { kind: 'note', path: 'knowledge/bar.md', anchor: undefined }],
    ['../../../x.md', 'a', { kind: 'note', path: 'x.md', anchor: undefined }],
    ['/abs.md', 'knowledge/areas', { kind: 'note', path: 'abs.md', anchor: undefined }],
    ['/knowledge/areas/foo.md', '', { kind: 'note', path: 'knowledge/areas/foo.md', anchor: undefined }],
    ['foo.md?x=1#sec', 'knowledge/areas', { kind: 'note', path: 'knowledge/areas/foo.md', anchor: 'sec' }],
    ['/knowledge/foo.md?v=2', '', { kind: 'note', path: 'knowledge/foo.md', anchor: undefined }],
    ['/knowledge/foo.md#Sec%20One', '', { kind: 'note', path: 'knowledge/foo.md', anchor: 'Sec One' }],
    ['#Sec%20One', 'knowledge/areas', { kind: 'fragment', anchor: 'Sec One' }],
    ['https://example.com', '', { kind: 'external', href: 'https://example.com' }],
    ['http://example.com', 'knowledge', { kind: 'external', href: 'http://example.com' }],
    ['//example.com/x', '', { kind: 'external', href: '//example.com/x' }],
    ['mailto:someone@example.com', '', { kind: 'external', href: 'mailto:someone@example.com' }],
    ['file:///etc/hosts', '', { kind: 'external', href: 'file:///etc/hosts' }],
    ['', 'knowledge/areas', { kind: 'external', href: '' }],
    ['   ', 'knowledge/areas', { kind: 'external', href: '' }],
  ])('resolves %j from %j', (href, baseDir, resolved) => {
    expect(resolveNotebookLink(href, baseDir)).toEqual(resolved);
  });
});

describe('headingSlug', () => {
  it.each([
    ['My Heading!', 'my-heading'],
    ['A   B', 'a-b'],
    ['  Setup: step 2  ', 'setup-step-2'],
  ])('slugs %j as %j', (heading, slug) => {
    expect(headingSlug(heading)).toBe(slug);
  });
});
