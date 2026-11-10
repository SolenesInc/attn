/** The document a MarkdownReader renders. `uri` is opaque identity; typed fields
 * are the authority, and neither side parses a path or seed id out of the URI. */
export interface FileMarkdownDocumentSource {
  kind: 'file';
  uri: string;
  path: string;
  /** The outpost that holds the file; absent for the home. */
  host?: string;
}

export interface SeedMarkdownDocumentSource {
  kind: 'seed';
  uri: `attn://seed/${string}`;
  seedId: string;
}

export type MarkdownDocumentSource = FileMarkdownDocumentSource | SeedMarkdownDocumentSource;

export function markdownFileDocumentUri(path: string, host?: string): string {
  const uri = `attn://file/${encodeURIComponent(path)}`;
  return host ? `${uri}?host=${encodeURIComponent(host)}` : uri;
}

export function fileMarkdownSource(path: string, host?: string): FileMarkdownDocumentSource {
  return host
    ? { kind: 'file', uri: markdownFileDocumentUri(path, host), path, host }
    : { kind: 'file', uri: markdownFileDocumentUri(path), path };
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
