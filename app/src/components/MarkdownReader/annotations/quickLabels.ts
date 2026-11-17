// The shared label set (`src/annotations/quickLabels.ts`), adapted for the Markdown
// reader: a mark stores `quickLabelId`, never the label's text baked into the comment.

import { labelById, type QuickLabel } from '../../../annotations/quickLabels';

export {
  labelChipStyle,
  PROMOTED_LABELS,
  QUICK_LABEL_PICKER_GROUPS,
  QUICK_LABEL_PICKER_LABELS,
  QUICK_LABELS,
  type QuickLabel,
} from '../../../annotations/quickLabels';

export function quickLabelById(id: string): QuickLabel | undefined {
  return labelById(id);
}
