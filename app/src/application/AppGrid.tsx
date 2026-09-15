import { GridView } from '../components/grid/GridView';
import { useAppContext } from './AppContext';

export function AppGrid() {
  const {
    view,
    visibleGridTiles,
    resolvedGridLayout,
    gridOffBoardCount,
    hiddenGridSessions,
    handleRemoveFromGrid,
    handleRestoreToGrid,
    resolvedTheme,
    getScreenSnapshot,
  } = useAppContext();
  return (
    <>
      {view === 'grid' && (
        <div className="view-container visible">
          <GridView
            tiles={visibleGridTiles}
            layout={{ rows: resolvedGridLayout.rows, cols: resolvedGridLayout.cols }}
            offBoardCount={gridOffBoardCount}
            hiddenSessions={hiddenGridSessions}
            onRemoveTile={handleRemoveFromGrid}
            onRestoreTile={handleRestoreToGrid}
            resolvedTheme={resolvedTheme}
            getScreenSnapshot={getScreenSnapshot}
          />
        </div>
      )}
    </>
  );
}
