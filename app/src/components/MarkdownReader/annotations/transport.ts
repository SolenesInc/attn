/** Draft-persistence transport seam. Generation contract, mirroring
 * internal/store/markdown_annotations.go: a save at or below the tombstone is stale. */

import type { WireAnnotation } from './types';
import type { MarkdownDocumentSource } from '../documentSource';

/** `delivered` types into a session, `noted` appends to a seed; both tombstone-clear
 * drafts. `skipped_pending_approval` keeps the draft. */
export interface MarkdownAnnotationsSubmitResult {
  status: string;
  generation?: number;
  error?: string;
}

export type MarkdownAnnotationsDestination =
  | { kind: 'session'; sessionId: string }
  | { kind: 'seed'; seedId: string };

export interface MarkdownAnnotationsTransport {
  getMarkdownAnnotations(
    source: MarkdownDocumentSource,
  ): Promise<{ annotations: WireAnnotation[]; generation: number }>;
  saveMarkdownAnnotations(
    source: MarkdownDocumentSource,
    annotations: WireAnnotation[],
    generation: number,
  ): Promise<{ stale: boolean }>;
  clearMarkdownAnnotations(
    source: MarkdownDocumentSource,
    generation: number,
  ): Promise<{ generation: number }>;
  /** `orphanedIds` is client-derived and not persisted; the document URI stays
   * opaque identity. */
  submitMarkdownAnnotations(
    source: MarkdownDocumentSource,
    destination: MarkdownAnnotationsDestination,
    orphanedIds: string[],
  ): Promise<MarkdownAnnotationsSubmitResult>;
}

let currentTransport: MarkdownAnnotationsTransport | null = null;

export function setMarkdownAnnotationsTransport(
  transport: MarkdownAnnotationsTransport | null,
): void {
  currentTransport = transport;
}

export function getMarkdownAnnotationsTransport(): MarkdownAnnotationsTransport | null {
  return currentTransport;
}
