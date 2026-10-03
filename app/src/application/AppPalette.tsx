import { useCallback, useMemo } from 'react';
import type { PaletteState } from '../components/palette/paletteState';
import { UnifiedPalette } from '../components/palette/UnifiedPalette';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { tileContentKey, type TileLeaf } from '../types/desktop';
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
  const { palette, setPalette } = useAppPanelsContext();
  if (palette === null) return null;
  return <OpenPalette state={palette} onStateChange={setPalette} onClose={() => setPalette(null)} />;
}

function OpenPalette({
  state,
  onStateChange,
  onClose,
}: {
  state: PaletteState;
  onStateChange: (state: PaletteState) => void;
  onClose: () => void;
}) {
  const { desktopViews, handleSelectSession, handleSelectTile } = useNavigationContext();
  const { crewQueueEnabled } = useAttentionQueueContext();
  const { handleWakeCrewMember } = useAppGardenActionsContext();
  const { desktopTileContents, sendSettleTurn, sendSnoozeTurn } = useDaemonApi();
  const desktops = useProfilesStore((state) => state.desktops);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const crew = useDaemonStore((state) => state.crew);
  const seeds = useDaemonStore((state) => state.seeds);
  const commands = useAppCommands();

  const tileTitle = useCallback(
    (desktopId: string, tile: TileLeaf) =>
      deriveTileTitle(tile, desktopTileContents[tileContentKey(desktopId, tile.tileId)],
        (id) => seeds.find((seed) => seed.id === id)?.title),
    [desktopTileContents, seeds],
  );
  const agents = useMemo(() => {
    const now = Date.now();
    return {
      bands: buildQueueBands(desktopViews, { crewInQueue: crewQueueEnabled, now }),
      crewRoster: crew.filter((member) => member.profile_id === selectedProfileId).map((member) => member.id),
      desktops: desktopViews,
      tileTitle,
      now,
    };
  }, [crew, crewQueueEnabled, desktopViews, selectedProfileId, tileTitle]);

  return (
    <UnifiedPalette
      state={state}
      onStateChange={onStateChange}
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
