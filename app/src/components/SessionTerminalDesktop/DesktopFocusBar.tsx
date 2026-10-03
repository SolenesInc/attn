import { formatShortcut } from '../../shortcuts/formatShortcut';
import { useDesktopContext } from './DesktopContext';
export function DesktopFocusBar() {
  const {
    tileLeafById,
    effectivePaneId,
    setMaximizedLeafId,
    focusDocument,
    focusModeTitle,
    reviewDeckTiles,
  } = useDesktopContext();

  return (
    <>
      {' '}
      {effectivePaneId && (
        <div className="desktop-focus-bar">
          <div className="desktop-focus-label">
            <span className="desktop-focus-kicker">Focus</span>
            {tileLeafById.has(effectivePaneId) && reviewDeckTiles.length > 1 ? (
              <div
                className="desktop-focus-tabs"
                role="tablist"
                aria-label="Open review documents"
              >
                {reviewDeckTiles.map((tile) => {
                  const label =
                    (tile.tileParams ?? '').split('/').filter(Boolean).pop() ||
                    tile.tileKind ||
                    'Document';
                  const selected = tile.tileId === effectivePaneId;
                  return (
                    <button
                      key={tile.tileId}
                      type="button"
                      className={`desktop-focus-tab ${selected ? 'desktop-focus-tab--selected' : ''}`.trim()}
                      role="tab"
                      aria-selected={selected}
                      onClick={() => focusDocument(tile.tileId)}
                    >
                      {label}
                    </button>
                  );
                })}
              </div>
            ) : (
              <span className="desktop-focus-title">{focusModeTitle}</span>
            )}
          </div>
          <button
            type="button"
            className="desktop-focus-exit"
            onClick={() => setMaximizedLeafId(null)}
            title={`Exit focus mode (${formatShortcut('terminal.toggleMaximize')})`}
          >
            Return to split
          </button>
        </div>
      )}
    </>
  );
}
