const annotationDrafts = new Map<string, string>();

export function readAnnotationDraft(draftKey: string): string | undefined {
  return annotationDrafts.get(draftKey);
}

export function writeAnnotationDraft(draftKey: string, text: string): void {
  annotationDrafts.set(draftKey, text);
}

export function clearAnnotationDraft(draftKey: string): void {
  annotationDrafts.delete(draftKey);
}

export function peekAnnotationDraft(draftKey: string): string | undefined {
  return annotationDrafts.get(draftKey);
}
