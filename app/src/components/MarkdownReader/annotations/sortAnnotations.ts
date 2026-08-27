import type { Annotation } from './types';

export function sortAnnotations(annotations: Annotation[]): Annotation[] {
  return [...annotations].sort((a, b) => {
    if (!a.anchor && !b.anchor) {
      return a.createdAt - b.createdAt;
    }
    if (!a.anchor) {
      return 1;
    }
    if (!b.anchor) {
      return -1;
    }
    if (a.anchor.startLine !== b.anchor.startLine) {
      return a.anchor.startLine - b.anchor.startLine;
    }
    return a.anchor.start - b.anchor.start;
  });
}
