import { PaneAskChip } from '../Asks/PaneAskChip';
import { HeaderSettleKeptChip, HeaderSettlingIndicator } from '../SettlingIndicator';
import { useDesktopContext } from './DesktopContext';
import { FitPiece } from './FitPiece';
import type { HeaderLevels } from './headerFit';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopAgentSignals({ agentPane, paneSession, levels }: Omit<DesktopAgentProps, 'paneTitle'> & { levels: HeaderLevels }) {
  const { onOpenAsk, onCancelCountdown } = useDesktopContext();
  const autoSettleFiresAt = paneSession?.autoSettleFiresAt;
  const autoSettleHeld = paneSession?.autoSettleHeld;
  const autoSettleDismissArmed = paneSession?.autoSettleDismissArmed;

  return (
    <span className="desktop-pane-signals">
      <FitPiece piece="settling" levels={levels} fitLevels={['full', 'icon']}>
        {autoSettleFiresAt || autoSettleHeld ? (
          <HeaderSettlingIndicator
            firesAt={autoSettleFiresAt}
            held={autoSettleHeld}
            frozen={paneSession?.offline}
            onCancel={() => onCancelCountdown?.(agentPane.sessionId)}
          />
        ) : autoSettleDismissArmed && !paneSession?.offline ? (
          <HeaderSettleKeptChip onDisarm={() => onCancelCountdown?.(agentPane.sessionId)} />
        ) : null}
      </FitPiece>
      <FitPiece piece="ask" levels={levels} fitLevels={['full', 'short', 'icon']}>
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
      </FitPiece>
    </span>
  );
}
