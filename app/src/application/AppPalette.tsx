import { useCallback, useEffect, useMemo, useState } from 'react';
import type { CommandUsage } from '../types/generated';
import { useToastStore } from '../store/toasts';
import type { PaletteState } from '../components/palette/paletteState';
import { UnifiedPalette } from '../components/palette/UnifiedPalette';
import { RenamePopover } from '../components/RenamePopover';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { tileContentKey, type TileLeaf } from '../types/desktop';
import { buildQueueBands } from '../utils/queueBands';
import { deriveTileTitle } from '../utils/tilePresentation';
import {
  useAppGardenActionsContext,
  useAppPanelsContext,
  useAppInputs,
  useAttentionQueueContext,
  useNavigationContext,
} from './AppContexts';
import { useAppCommands } from './useAppCommands';

export function AppPalette() {
  const profileId = useProfilesStore((state) => state.selectedProfileId);
  return <ProfilePalette key={profileId} />;
}

function ProfilePalette() {
  const { palette, setPalette } = useAppPanelsContext();
  const { sendRenameSession } = useDaemonApi();
  const { daemonSessions: sessions } = useAppInputs();
  const [renameTarget, setRenameTarget] = useState<{
    sessionId: string;
    name: string;
    anchor: { top: number; left: number };
    opener: HTMLElement | null;
  } | null>(null);
  const openRename = useCallback((sessionId: string, name: string, opener: HTMLElement | null) => {
    const rect = (opener ?? document.body).getBoundingClientRect();
    setRenameTarget({ sessionId, name, anchor: { top: rect.top, left: rect.left }, opener });
  }, []);
  return <>
    {palette !== null && <OpenPalette state={palette} onStateChange={setPalette} onClose={() => setPalette(null)} onRename={openRename} />}
    {renameTarget && <RenamePopover
      key={renameTarget.sessionId}
      initialValue={renameTarget.name}
      defaultName={sessions.find((session) => session.id === renameTarget.sessionId)?.agent === 'shell' ? 'Automatic title' : undefined}
      label="Rename session"
      anchor={renameTarget.anchor}
      onSubmit={(value) => sendRenameSession(renameTarget.sessionId, value)}
      onClose={() => {
        setRenameTarget(null);
        renameTarget.opener?.focus({ preventScroll: true });
      }}
    />}
  </>;
}

function OpenPalette({
  state,
  onStateChange,
  onClose,
  onRename,
}: {
  state: PaletteState;
  onStateChange: (state: PaletteState) => void;
  onClose: () => void;
  onRename: (sessionId: string, name: string, opener: HTMLElement | null) => void;
}) {
  const { desktopViews, handleSelectSession, handleSelectTile } = useNavigationContext();
  const { queueModeEnabled, crewQueueEnabled, queueHidden } = useAttentionQueueContext();
  const { handleWakeCrewMember } = useAppGardenActionsContext();
  const { desktopTileContents, sendSettleTurn, sendSnoozeTurn, sendSetSessionPriority, sendGetCommandUsage, sendRecordCommandUsage } = useDaemonApi();
  const desktops = useProfilesStore((state) => state.desktops);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const crew = useDaemonStore((state) => state.crew);
  const seeds = useDaemonStore((state) => state.seeds);
  const commands = useAppCommands(onRename);
  const [usage, setUsage] = useState<CommandUsage[] | null>(null);
  useEffect(() => {
    let current = true;
    if (!selectedProfileId) {
      setUsage([]);
      return;
    }
    void sendGetCommandUsage(selectedProfileId).then(
      (entries) => { if (current) setUsage(entries); },
      (error: Error) => {
        if (!current) return;
        useToastStore.getState().append({ source: 'Command history', message: error.message });
        setUsage([]);
      },
    );
    return () => { current = false; };
  }, [selectedProfileId, sendGetCommandUsage]);

  const recordCommand = (commandId: string) => {
    if (!selectedProfileId) return;
    void sendRecordCommandUsage(selectedProfileId, commandId).catch((error: Error) => {
      useToastStore.getState().append({ source: 'Command history', message: error.message });
    });
  };

  const tileTitle = useCallback(
    (desktopId: string, tile: TileLeaf) =>
      deriveTileTitle(tile, desktopTileContents[tileContentKey(desktopId, tile.tileId)],
        (id) => seeds.find((seed) => seed.id === id)?.title),
    [desktopTileContents, seeds],
  );
  const agents = useMemo(() => {
    const now = Date.now();
    return {
      bands: buildQueueBands(desktopViews, { crewInQueue: crewQueueEnabled, now, hidden: queueHidden }),
      crewRoster: crew.filter((member) => member.profile_id === selectedProfileId).map((member) => member.id),
      desktops: desktopViews,
      tileTitle,
      now,
    };
  }, [crew, crewQueueEnabled, desktopViews, queueHidden, selectedProfileId, tileTitle]);

  return (
    <UnifiedPalette
      state={state}
      onStateChange={onStateChange}
      onClose={onClose}
      agents={agents}
      queueMode={queueModeEnabled}
      crewInQueue={crewQueueEnabled}
      desktops={desktops}
      commands={commands}
      commandUsage={usage ?? []}
      commandsLoading={usage === null}
      onCommandPick={recordCommand}
      onOpenAgent={(session) => handleSelectSession(session.id)}
      onWakeMember={handleWakeCrewMember}
      onOpenTile={handleSelectTile}
      onSettle={(session) => void sendSettleTurn(session.id)}
      onPriority={(session) => sendSetSessionPriority(session.id, !session.priority)}
      onSnooze={(session, until) => void sendSnoozeTurn(session.id, until)}
    />
  );
}
