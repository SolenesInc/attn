import { GridView } from '../components/grid/GridView';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAppAppearanceContext, useAppGridContext, useNavigationContext } from './AppContexts';

export function AppGrid() {
  const { view } = useNavigationContext();
  const {
    visibleGridTiles,
    resolvedGridLayout,
    gridOffBoardCount,
    hiddenGridSessions,
    handleRemoveFromGrid,
    handleRestoreToGrid,
  } = useAppGridContext();
  const { resolvedTheme } = useAppAppearanceContext();
  const { getScreenSnapshot } = useDaemonApi();
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
