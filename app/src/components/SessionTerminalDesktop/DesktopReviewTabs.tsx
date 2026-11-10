import { tileParamsPath } from '../../utils/tilePresentation';
import { useDesktopContext } from './DesktopContext';

export function DesktopReviewTabs() {
  const { effectivePaneId, focusDocument, reviewDeckTiles } = useDesktopContext();
  if (reviewDeckTiles.length < 2 || !reviewDeckTiles.some((tile) => tile.tileId === effectivePaneId)) return null;
  return (
    <div className="desktop-review-tabs" role="tablist" aria-label="Open review documents">
      {reviewDeckTiles.map((tile) => {
        const label = tileParamsPath(tile).split('/').filter(Boolean).pop() || tile.tileKind || 'Document';
        const selected = tile.tileId === effectivePaneId;
        return (
          <button
            key={tile.tileId}
            type="button"
            className={`desktop-review-tab ${selected ? 'desktop-review-tab--selected' : ''}`.trim()}
            role="tab"
            aria-selected={selected}
            onClick={() => focusDocument(tile.tileId)}
          >
            {label}
          </button>
        );
      })}
    </div>
  );
}
