import { renderPrompt } from "../../prompts/render";
// The label set is shared with the Markdown reader (`src/annotations/quickLabels.ts`);
// this module owns only the payload.

export {
  QUICK_LABEL_GROUPS,
  QUICK_LABELS,
  labelById,
  type QuickLabel,
} from "../../annotations/quickLabels";

import { labelById } from "../../annotations/quickLabels";

export interface PayloadAnnotation {
  quote: string;
  quickLabelId: string;
  comment: string;
  start: number;
}

export function buildAnnotationPayload(
  annotations: readonly PayloadAnnotation[],
  note = "",
): string {
  if (annotations.length === 0) return "";
  const ordered = [...annotations].sort((a, b) => a.start - b.start);
  const entries = ordered
    .map((annotation, index) => {
      const label = annotation.quickLabelId
        ? labelById(annotation.quickLabelId)
        : undefined;
      return renderPrompt("annotation-terminal", "entry", {
        index: String(index + 1),
        heading: label ? `${label.emoji} ${label.text}` : "",
        quote: annotation.quote.split("\n").join("\n> "),
        tip: label?.tip ?? "",
        comment: annotation.comment,
        has_comment: String(annotation.comment !== ""),
      });
    })
    .join("");
  return renderPrompt("annotation-terminal", "submit", {
    note: note.trim(),
    entries,
  }).trimEnd();
}
