import { useLayoutEffect, useRef, useState } from 'react';
import { navTitle, type NavAnswer } from './navigation';
import { PathLabel } from './StopBlock';

export const NAV_ROW_HEIGHT = 22;
const OVERSCAN_ROWS = 20;

export function NavPanel({ answer, onPick }: { answer: NavAnswer; onPick: (index: number) => void }) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [view, setView] = useState({ top: 0, height: 0 });
  const { locations } = answer;

  useLayoutEffect(() => {
    const element = scrollRef.current;
    if (!element) return;
    const rowTop = answer.cursor * NAV_ROW_HEIGHT;
    if (rowTop < element.scrollTop || rowTop + NAV_ROW_HEIGHT > element.scrollTop + element.clientHeight) {
      element.scrollTop = Math.max(0, rowTop - element.clientHeight / 3);
    }
    setView({ top: element.scrollTop, height: element.clientHeight });
  }, [answer.cursor, locations]);

  const first = Math.max(0, Math.floor(view.top / NAV_ROW_HEIGHT) - OVERSCAN_ROWS);
  const last = Math.min(locations.length, Math.ceil((view.top + Math.max(view.height, NAV_ROW_HEIGHT)) / NAV_ROW_HEIGHT) + OVERSCAN_ROWS);
  const title = navTitle(answer.kind, answer.name);

  return (
    <div className="review-nav-panel" role="dialog" aria-label={title}>
      <header className="review-nav-panel-header">
        <span>{title}</span>
        {answer.kind !== 'hover' ? <span className="review-nav-panel-count">{locations.length}</span> : null}
        <span className="review-grow" />
        {locations.length > 0 ? <span><kbd>↵</kbd> open</span> : null}
        <span><kbd>esc</kbd> close</span>
      </header>
      {answer.text ? <pre className="review-nav-panel-text">{answer.text}</pre> : null}
      {locations.length > 0 ? (
        <div className="review-nav-panel-list" ref={scrollRef} role="listbox" aria-label={title} onScroll={(event) => setView({ top: event.currentTarget.scrollTop, height: event.currentTarget.clientHeight })}>
          <div className="review-nav-panel-rows" style={{ height: locations.length * NAV_ROW_HEIGHT }}>
            {locations.slice(first, last).map((location, offset) => {
              const index = first + offset;
              return (
                <div
                  key={`${location.path}:${location.line}:${location.character}`}
                  className={`review-nav-panel-row${index === answer.cursor ? ' review-cursor' : ''}`}
                  role="option"
                  aria-selected={index === answer.cursor}
                  style={{ top: index * NAV_ROW_HEIGHT, height: NAV_ROW_HEIGHT }}
                  onClick={() => onPick(index)}
                >
                  <span className="review-nav-panel-place"><PathLabel path={location.path} />:{location.line}</span>
                  {location.outside ? <span className="review-nav-panel-outside">outside</span> : null}
                  <span className="review-nav-panel-line md-shiki">{location.text.trim()}</span>
                </div>
              );
            })}
          </div>
        </div>
      ) : null}
    </div>
  );
}
