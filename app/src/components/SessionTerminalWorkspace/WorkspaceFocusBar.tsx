import { formatShortcut } from '../../shortcuts/formatShortcut';
import { useWorkspaceContext } from './WorkspaceContext';
export function WorkspaceFocusBar() {
  const {
    tileLeafById,
    effectivePaneId,
    setMaximizedLeafId,
    focusDocument,
    focusModeTitle,
    reviewDeckTiles,
  } = useWorkspaceContext();

  return (
    <>
      {' '}
      {effectivePaneId && (
        <div className="workspace-focus-bar">
          <div className="workspace-focus-label">
            <span className="workspace-focus-kicker">Focus</span>
            {tileLeafById.has(effectivePaneId) && reviewDeckTiles.length > 1 ? (
              <div
                className="workspace-focus-tabs"
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
                      className={`workspace-focus-tab ${selected ? 'workspace-focus-tab--selected' : ''}`.trim()}
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
              <span className="workspace-focus-title">{focusModeTitle}</span>
            )}
          </div>
          <button
            type="button"
            className="workspace-focus-exit"
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
