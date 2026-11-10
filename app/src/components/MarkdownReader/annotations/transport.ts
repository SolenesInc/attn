/** Draft-persistence transport seam. The daemon owns the draft: a save carries one
 * reader's changes, and every reply carries the draft as it then stands. */

import type { WireAnnotation } from './types';
import type { MarkdownDocumentSource } from '../documentSource';

export interface MarkdownAnnotationsDraft {
  annotations: WireAnnotation[];
  generation: number;
}

/** `delivered` types into a session, `noted` appends to a seed; both remove what
 * went from the draft and answer with `draft`. `skipped_pending_approval` keeps it. */
export interface MarkdownAnnotationsSubmitResult {
  status: string;
  draft?: MarkdownAnnotationsDraft;
  error?: string;
}

/** A send from a review document: a question asked alone, or the review with its verdict. */
export interface MarkdownAnnotationsSubmitOptions {
  onlyIds?: string[];
  verdict?: string;
  /** The review snapshot the user read. */
}

export type MarkdownAnnotationsDestination =
  | { kind: 'session'; sessionId: string }
  | { kind: 'seed'; seedId: string };

export interface MarkdownAnnotationsTransport {
  getMarkdownAnnotations(source: MarkdownDocumentSource): Promise<MarkdownAnnotationsDraft>;
  saveMarkdownAnnotations(
    source: MarkdownDocumentSource,
    upserts: WireAnnotation[],
    removedIds: string[],
  ): Promise<MarkdownAnnotationsDraft>;
  /** `orphanedIds` is client-derived and not persisted; the document URI stays
   * opaque identity. */
  submitMarkdownAnnotations(
    source: MarkdownDocumentSource,
    destination: MarkdownAnnotationsDestination,
    orphanedIds: string[],
    options?: MarkdownAnnotationsSubmitOptions,
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
