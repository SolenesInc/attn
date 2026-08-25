// The label set is shared with the Markdown reader (`src/annotations/quickLabels.ts`);
// this module owns only the payload.

export {
  QUICK_LABEL_GROUPS,
  QUICK_LABELS,
  labelById,
  type QuickLabel,
} from '../../annotations/quickLabels';

import { labelById } from '../../annotations/quickLabels';

export interface PayloadAnnotation {
  quote: string;
  quickLabelId: string;
  comment: string;
  start: number;
}

export function buildAnnotationPayload(
  annotations: readonly PayloadAnnotation[],
  note = '',
): string {
  if (annotations.length === 0) return '';
  const ordered = [...annotations].sort((a, b) => a.start - b.start);
  const lines: string[] = ['Feedback on your last message.', ''];
  const trimmedNote = note.trim();
  if (trimmedNote) lines.push(trimmedNote, '');
  ordered.forEach((annotation, index) => {
    const label = annotation.quickLabelId ? labelById(annotation.quickLabelId) : undefined;
    const heading = label
      ? `${label.emoji} ${label.text}`
      : '💬 Comment';
    lines.push(`## ${index + 1}. ${heading}`, '');
    lines.push(`> ${annotation.quote.split('\n').join('\n> ')}`, '');
    if (label?.tip) lines.push(label.tip);
    if (annotation.comment) lines.push(annotation.comment);
    lines.push('');
  });
  return lines.join('\n').trimEnd();
}
