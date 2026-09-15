import { HeaderNudgeIndicator, deriveNudgeMode } from '../NudgeIndicator';
import { PaneSeedChip } from '../PaneSeedChip';
import { HeaderPresentationChip } from '../PresentationChip';
import { HeaderSettleKeptChip, HeaderSettlingIndicator } from '../SettlingIndicator';
import { derivePaneSeedDisplay } from '../paneSeedDisplay';
import { useWorkspaceContext } from './WorkspaceContext';
import type { WorkspaceAgentProps } from './workspaceTypes';
export function WorkspaceAgentSignals({ agentPane, paneSession }: WorkspaceAgentProps) {
  const {
    gardenSeeds,
    onOpenSeed,
    onTriggerNudge,
    onCancelCountdown,
    onOpenPresentation,
    pinnedSeedPopover,
    dismissSeedPopover,
  } = useWorkspaceContext();
  const nudgeMode = paneSession?.state
    ? deriveNudgeMode({
        ticketUnread: paneSession.ticketUnread,
        nudgeFiresAt: paneSession.nudgeFiresAt,
        state: paneSession.state,
        isActive: Boolean(paneSession.isActive),
      })
    : null;
  const paneSeedDisplay = derivePaneSeedDisplay(
    gardenSeeds,
    agentPane.sessionId,
    paneSession?.seedId,
    paneSession?.crewMember,
  );
  const autoSettleFiresAt = paneSession?.autoSettleFiresAt;
  const autoSettleHeld = paneSession?.autoSettleHeld;
  const autoSettleDismissArmed = paneSession?.autoSettleDismissArmed;

  return (
    <>
      {' '}
      {paneSession?.presentation ? (
        <HeaderPresentationChip
          presentation={paneSession.presentation}
          onOpen={(presentationId) => onOpenPresentation?.(presentationId)}
        />
      ) : null}
      {autoSettleFiresAt || autoSettleHeld ? (
        <HeaderSettlingIndicator
          firesAt={autoSettleFiresAt}
          held={autoSettleHeld}
          onCancel={() => onCancelCountdown?.(agentPane.sessionId)}
        />
      ) : autoSettleDismissArmed ? (
        <HeaderSettleKeptChip onDisarm={() => onCancelCountdown?.(agentPane.sessionId)} />
      ) : null}
      {nudgeMode ? (
        <HeaderNudgeIndicator
          mode={nudgeMode}
          firesAt={paneSession?.nudgeFiresAt}
          onTrigger={() => onTriggerNudge?.(agentPane.sessionId)}
          onCancel={() => onCancelCountdown?.(agentPane.sessionId)}
        />
      ) : null}
      {paneSeedDisplay.kind !== 'none' && onOpenSeed ? (
        <PaneSeedChip
          display={paneSeedDisplay}
          crownSeedId={paneSession?.seedId}
          unread={Boolean(paneSession?.ticketUnread)}
          sessionId={agentPane.sessionId}
          pinned={pinnedSeedPopover === agentPane.sessionId}
          onOpenSeed={onOpenSeed}
          onPopoverClosed={dismissSeedPopover}
        />
      ) : null}
    </>
  );
}
