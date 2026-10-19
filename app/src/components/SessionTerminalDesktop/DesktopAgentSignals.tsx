import { PaneAskChip } from '../Asks/PaneAskChip';
import { PaneSeedChip } from '../PaneSeedChip';
import { HeaderSettleKeptChip, HeaderSettlingIndicator } from '../SettlingIndicator';
import { derivePaneSeedDisplay } from '../paneSeedDisplay';
import { useDesktopContext } from './DesktopContext';
import type { DesktopAgentProps } from './desktopTypes';
export function DesktopAgentSignals({ agentPane, paneSession }: DesktopAgentProps) {
  const {
    gardenSeeds,
    onOpenSeed,
    onOpenAsk,
    onCancelCountdown,
    pinnedSeedPopover,
    dismissSeedPopover,
  } = useDesktopContext();
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
      {autoSettleFiresAt || autoSettleHeld ? (
        <HeaderSettlingIndicator
          firesAt={autoSettleFiresAt}
          held={autoSettleHeld}
          onCancel={() => onCancelCountdown?.(agentPane.sessionId)}
        />
      ) : autoSettleDismissArmed ? (
        <HeaderSettleKeptChip onDisarm={() => onCancelCountdown?.(agentPane.sessionId)} />
      ) : null}
      {paneSeedDisplay.kind !== 'none' && onOpenSeed ? (
        <PaneSeedChip
          display={paneSeedDisplay}
          crownSeedId={paneSession?.seedId}
          unread={false}
          sessionId={agentPane.sessionId}
          pinned={pinnedSeedPopover === agentPane.sessionId}
          onOpenSeed={onOpenSeed}
          onPopoverClosed={dismissSeedPopover}
        />
      ) : null}
      {onOpenAsk ? (
        <PaneAskChip
          session={{
            id: agentPane.sessionId,
            crewMember: paneSession?.crewMember,
            chiefOfStaff: paneSession?.chiefOfStaff,
            seedId: paneSession?.seedId,
          }}
          onOpenAsk={onOpenAsk}
        />
      ) : null}
    </>
  );
}
