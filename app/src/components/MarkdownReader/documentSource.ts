/** The document a MarkdownReader renders. `uri` is opaque identity; typed fields
 * are the authority, and neither side parses a path or seed id out of the URI. */
export interface FileMarkdownDocumentSource {
  kind: 'file';
  uri: string;
  path: string;
}

export interface SeedMarkdownDocumentSource {
  kind: 'seed';
  uri: `attn://seed/${string}`;
  seedId: string;
}

export type MarkdownDocumentSource = FileMarkdownDocumentSource | SeedMarkdownDocumentSource;

export function markdownFileDocumentUri(path: string): string {
  return `attn://file/${encodeURIComponent(path)}`;
}

export function fileMarkdownSource(path: string): FileMarkdownDocumentSource {
  return {
    kind: 'file',
    uri: markdownFileDocumentUri(path),
    path,
  };
}

export function seedMarkdownSource(seedId: string): SeedMarkdownDocumentSource {
  return {
    kind: 'seed',
    uri: `attn://seed/${encodeURIComponent(seedId)}`,
    seedId,
  };
}

/** Files resolve relative targets beside themselves; seeds have no directory. */
export function markdownDocumentPath(source: MarkdownDocumentSource): string {
  return source.kind === 'file' ? source.path : '';
}
