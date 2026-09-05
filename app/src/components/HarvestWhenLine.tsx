import { openUrl } from '@tauri-apps/plugin-opener';
import type { HarvestWhen } from '../types/generated';
import { harvestWhenDisplay } from '../utils/harvestWhen';
import './HarvestWhenLine.css';

/** The whole condition, beside a seed's state, wherever a seed is read in full. */
export function HarvestWhenLine({ condition }: { condition?: HarvestWhen }) {
  const display = harvestWhenDisplay(condition);
  if (!display) return null;
  if (!display.url) return <span className="harvest-when">{display.sentence}</span>;
  return (
    <a
      className="harvest-when"
      href={display.url}
      data-harvest-when={display.pullRequest}
      onClick={(event) => {
        event.preventDefault();
        void openUrl(display.url).catch((error) => {
          console.warn('[HarvestWhenLine] Failed to open the pull request:', error);
        });
      }}
    >
      {display.sentence}
      <span aria-label="opens outside attn">↗</span>
    </a>
  );
}
