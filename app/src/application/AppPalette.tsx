import { useCallback, useMemo } from 'react';
import { UnifiedPalette } from '../components/palette/UnifiedPalette';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useAppViewTitleResolver } from '../hooks/useAppViewTitle';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { tileContentKey, type TileLeaf } from '../types/workspace';
import { buildQueueBands } from '../utils/queueBands';
import { deriveTileTitle } from '../utils/tilePresentation';
import {
  useAppGardenActionsContext,
  useAppPanelsContext,
  useAttentionQueueContext,
  useNavigationContext,
} from './AppContexts';
import { useAppCommands } from './useAppCommands';

export function AppPalette() {
  const { paletteQuery, setPaletteQuery } = useAppPanelsContext();
  if (paletteQuery === null) return null;
  return <OpenPalette query={paletteQuery} onQueryChange={setPaletteQuery} onClose={() => setPaletteQuery(null)} />;
}

function OpenPalette({
  query,
  onQueryChange,
  onClose,
}: {
  query: string;
  onQueryChange: (query: string) => void;
  onClose: () => void;
}) {
  const { desktopViews, handleSelectSession, handleSelectTile } = useNavigationContext();
  const { crewQueueEnabled } = useAttentionQueueContext();
  const { handleWakeCrewMember } = useAppGardenActionsContext();
  const { desktopTileContents, sendSettleTurn, sendSnoozeTurn } = useDaemonApi();
  const desktops = useProfilesStore((state) => state.desktops);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const crew = useDaemonStore((state) => state.crew);
  const appViewTitle = useAppViewTitleResolver();
  const commands = useAppCommands();

  const tileTitle = useCallback(
    (desktopId: string, tile: TileLeaf) =>
      deriveTileTitle(tile, desktopTileContents[tileContentKey(desktopId, tile.tileId)], appViewTitle),
    [appViewTitle, desktopTileContents],
  );
  const agents = useMemo(() => {
    const now = Date.now();
    return {
      bands: buildQueueBands(desktopViews, { crewInQueue: crewQueueEnabled, now }),
      crewRoster: crew.filter((member) => member.profile_id === selectedProfileId).map((member) => member.id),
      workspaces: desktopViews,
      tileTitle,
      now,
    };
  }, [crew, crewQueueEnabled, desktopViews, selectedProfileId, tileTitle]);

  return (
    <UnifiedPalette
      query={query}
      onQueryChange={onQueryChange}
      onClose={onClose}
      agents={agents}
      desktops={desktops}
      commands={commands}
      onOpenAgent={(session) => handleSelectSession(session.id)}
      onWakeMember={handleWakeCrewMember}
      onOpenTile={handleSelectTile}
      onSettle={(session) => void sendSettleTurn(session.id)}
      onSnooze={(session, until) => void sendSnoozeTurn(session.id, until)}
    />
  );
}
