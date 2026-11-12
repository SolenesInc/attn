/** Marks a document block the Map's detail pane leaves out. */
export const OUTSIDE_STEP = 'data-review-outside';

const isStepHeading = (block: Element) => block.tagName === 'H1' || block.tagName === 'H2';

/** The blocks of a rendered review document the Map leaves out: every block
 * but the step that holds the stop, the change's summary, and the drawn
 * diagrams, which the Map draws beside the step. With no stop, every block. */
export function blocksOutsideStep(card: Element, stopLine: number | null, drawnLines: ReadonlySet<number>): Element[] {
  const blocks = Array.from(card.children);
  const at = stopLine === null ? -1 : blocks.findIndex((block) => block.getAttribute('data-review-stop-head') === String(stopLine));
  if (at < 0) return blocks;
  let start = at;
  while (start > 0 && !isStepHeading(blocks[start])) start -= 1;
  const next = blocks.findIndex((block, index) => index > at && isStepHeading(block));
  const end = next < 0 ? blocks.length : next;
  return blocks.filter((block, index) => index < start || index >= end
    || block.classList.contains('review-summary')
    || drawnLines.has(Number(block.getAttribute('data-source-line'))));
}

export function isOutsideStep(element: Element): boolean {
  return element.closest(`[${OUTSIDE_STEP}]`) !== null;
}
