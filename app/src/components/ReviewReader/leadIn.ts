const STOP = '[data-review-stop-head]';
const STEP_HEADING = 'h1, h2';

/** The first block that introduces a stop: the step's heading and its prose for
 * the first stop of a step, the prose since the previous stop for a later one,
 * the stop itself when nothing stands between. */
function leadInStart(stop: Element): Element {
  let start = stop;
  for (let block = stop.previousElementSibling; block !== null && !block.matches(STOP); block = block.previousElementSibling) {
    start = block;
    if (block.matches(STEP_HEADING)) break;
  }
  return start;
}

/** Puts a stop's lead-in at the top of the scroller, then scrolls the least
 * that shows the cursor when the lead-in pushed it out of view. */
export function showLeadIn(stop: HTMLElement, scroller: HTMLElement, cursor: HTMLElement): void {
  const view = () => scroller.getBoundingClientRect();
  scroller.scrollTop += leadInStart(stop).getBoundingClientRect().top - view().top;
  const { top, bottom } = cursor.getBoundingClientRect();
  const below = bottom - view().bottom;
  if (below > 0) scroller.scrollTop += Math.min(below, top - view().top);
}
