import type { MarkdownAnnotationsDraft } from './transport';
import { annotationToWire, type Annotation, type WireAnnotation } from './types';

/** What the daemon last said a reader's marks are. */
export type SyncedMarks = Map<string, Annotation>;

function wireForm(annotation: Annotation): string {
  return JSON.stringify(annotationToWire(annotation));
}

export function syncedMarks(annotations: Annotation[]): SyncedMarks {
  return new Map(annotations.map((annotation) => [annotation.id, annotation]));
}

function changedHere(annotation: Annotation, synced: SyncedMarks): boolean {
  const known = synced.get(annotation.id);
  return known === undefined || wireForm(known) !== wireForm(annotation);
}

/** What this reader changed since the daemon last answered it. */
export function unsavedChanges(
  local: Annotation[],
  synced: SyncedMarks,
): { upserts: WireAnnotation[]; removedIds: string[] } {
  const localIds = new Set(local.map((annotation) => annotation.id));
  return {
    upserts: local.filter((annotation) => changedHere(annotation, synced)).map(annotationToWire),
    removedIds: [...synced.keys()].filter((id) => !localIds.has(id)),
  };
}

function sameFeedback(a: Annotation, b: Annotation): boolean {
  return a.type === b.type && a.text === b.text && a.quickLabelId === b.quickLabelId && a.answer === b.answer
    && a.edit?.was === b.edit?.was && a.edit?.now === b.edit?.now
    && a.edit?.afterHead === b.edit?.afterHead && a.edit?.beforeHead === b.edit?.beforeHead;
}

/** The daemon's marks, with this reader's unsaved changes applied on top. A mark the
 * daemon dropped stays only if it is new here or the user rewrote it: a moved anchor
 * alone does not bring back a mark that was sent or removed. */
export function rebaseOnDraft(
  local: Annotation[],
  synced: SyncedMarks,
  draft: Annotation[],
): Annotation[] {
  const localById = new Map(local.map((annotation) => [annotation.id, annotation]));
  const draftIds = new Set(draft.map((annotation) => annotation.id));
  const removedHere = (annotation: Annotation) =>
    synced.has(annotation.id) && !localById.has(annotation.id);
  const worthKeeping = (annotation: Annotation) => {
    const known = synced.get(annotation.id);
    return known === undefined || !sameFeedback(known, annotation);
  };
  return [
    ...draft
      .filter((annotation) => !removedHere(annotation))
      .map((annotation) => {
        const mine = localById.get(annotation.id);
        return mine && changedHere(mine, synced) ? { ...mine, answer: annotation.answer } : annotation;
      }),
    ...local.filter((annotation) => !draftIds.has(annotation.id) && worthKeeping(annotation)),
  ];
}

export function sameMarks(a: Annotation[], b: Annotation[]): boolean {
  return a.length === b.length && a.every((annotation, index) => wireForm(annotation) === wireForm(b[index]));
}

type DraftListener = (draft: MarkdownAnnotationsDraft) => void;

const readersByDocument = new Map<string, Set<DraftListener>>();

/** Every reader of a document hears the draft any of them was answered with. */
export function listenToDocumentDraft(uri: string, listener: DraftListener): () => void {
  const readers = readersByDocument.get(uri) ?? new Set<DraftListener>();
  readersByDocument.set(uri, readers);
  readers.add(listener);
  return () => {
    readers.delete(listener);
    if (readers.size === 0) {
      readersByDocument.delete(uri);
    }
  };
}

export function announceDocumentDraft(uri: string, draft: MarkdownAnnotationsDraft): void {
  for (const listener of readersByDocument.get(uri) ?? []) {
    listener(draft);
  }
}
