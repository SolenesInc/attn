import type { Annotation } from './types';

function readingPlace(annotation: Annotation): [number, number, number] | null {
  if (annotation.anchor) return [annotation.anchor.startLine, 0, annotation.anchor.start];
  if (annotation.fileAnchor) return [annotation.fileAnchor.docLine, annotation.fileAnchor.from, 0];
  return null;
}

/** Marks in the order a reader meets them, general feedback last in the order it was written. */
export function sortAnnotations(annotations: Annotation[]): Annotation[] {
  return [...annotations].sort((a, b) => {
    const at = readingPlace(a);
    const bt = readingPlace(b);
    if (!at && !bt) {
      return a.createdAt - b.createdAt;
    }
    if (!at) {
      return 1;
    }
    if (!bt) {
      return -1;
    }
    return at[0] - bt[0] || at[1] - bt[1] || at[2] - bt[2];
  });
}
