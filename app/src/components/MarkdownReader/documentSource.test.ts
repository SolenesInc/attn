import { describe, expect, it } from 'vitest';
import {
  markdownDocumentPath,
  fileMarkdownSource,
  markdownFileDocumentUri,
  seedMarkdownSource,
} from './documentSource';

describe('Markdown document sources', () => {
  it('gives files an escaped identity carrying their path', () => {
    expect(markdownFileDocumentUri('/tmp/a plan.md')).toBe('attn://file/%2Ftmp%2Fa%20plan.md');
    expect(fileMarkdownSource('/tmp/plan.md')).toEqual({
      kind: 'file',
      uri: 'attn://file/%2Ftmp%2Fplan.md',
      path: '/tmp/plan.md',
    });
  });

  it('uses the canonical seed house URI without inventing a file path', () => {
    const source = seedMarkdownSource('s-7k3f9m');
    expect(source).toEqual({
      kind: 'seed',
      uri: 'attn://seed/s-7k3f9m',
      seedId: 's-7k3f9m',
    });
    expect(markdownDocumentPath(source)).toBe('');
  });
});
