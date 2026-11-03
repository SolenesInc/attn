import { useRef } from 'react';
import { DelegationChainTrigger } from '../DelegationChain';
import { PaneSeedChip } from '../PaneSeedChip';
import { SessionProvenance } from '../SessionProvenance';
import { harnessLabel } from '../harnessLabel';
import { derivePaneSeedDisplay, seedChipPresentation } from '../paneSeedDisplay';
import { HeaderSessionUsage } from './SessionUsage';
import { useDesktopContext } from './DesktopContext';
import { FitPiece } from './FitPiece';
import { SUBLINE_RANK, hostMark, titlesRepeat, type HeaderPiece } from './headerFit';
import { useHeaderFit } from './useHeaderFit';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopAgentSubline({ agentPane, paneSession, paneTitle }: DesktopAgentProps) {
  const {
    gardenSeeds,
    onOpenSeed,
    pinnedSeedPopover,
    dismissSeedPopover,
    pinnedUsagePopover,
    dismissUsagePopover,
    provenancePopoverOwner,
    setProvenancePopoverOwner,
    delegationSessionById,
    delegatesByDispatcherId,
  } = useDesktopContext();
  const rowRef = useRef<HTMLDivElement>(null);
  const keep: HeaderPiece[] = [];
  if (paneSession?.offline) keep.push('host');
  if (pinnedSeedPopover === agentPane.sessionId) keep.push('seed');
  if (pinnedUsagePopover === agentPane.sessionId) keep.push('usage');
  const levels = useHeaderFit(rowRef, SUBLINE_RANK, { keep });
  const delegationSession = delegationSessionById.get(agentPane.sessionId);
  const delegates = delegatesByDispatcherId.get(agentPane.sessionId) ?? [];
  const seedDisplay = derivePaneSeedDisplay(gardenSeeds, agentPane.sessionId, paneSession?.seedId);
  const hostName = paneSession?.host ? paneSession.hostName ?? paneSession.host : undefined;
  const seedLabel = seedChipPresentation(seedDisplay)?.label ?? '';

  return (
    <div ref={rowRef} className="desktop-pane-subline">
      <FitPiece piece="seed" levels={levels} fitLevels={['full', 'short', 'icon']}>
        {seedDisplay.kind !== 'none' && onOpenSeed ? (
          <PaneSeedChip
            display={seedDisplay}
            crownSeedId={paneSession?.seedId}
            unread={false}
            sessionId={agentPane.sessionId}
            pinned={pinnedSeedPopover === agentPane.sessionId}
            repeatsTitle={titlesRepeat(paneTitle, seedLabel)}
            onOpenSeed={onOpenSeed}
            onPopoverClosed={dismissSeedPopover}
          />
        ) : null}
      </FitPiece>
      <FitPiece piece="role" levels={levels} fitLevels={['full', 'icon']}>
        {delegationSession && (
          <DelegationChainTrigger
            session={delegationSession}
            hasDelegates={delegates.length > 0}
            variant="header"
            onOpen={() => setProvenancePopoverOwner(`${agentPane.id}:delegation`)}
          />
        )}
      </FitPiece>
      <FitPiece piece="host" levels={levels} fitLevels={['full', 'short']}>
        {hostName ? (
          <>
            <span className="desktop-pane-host">{hostName}</span>
            <span className="desktop-pane-host desktop-pane-host-mark" title={hostName} aria-hidden="true">{hostMark(hostName)}</span>
          </>
        ) : null}
      </FitPiece>
      <FitPiece piece="usage" levels={levels} fitLevels={['full']}>
        <HeaderSessionUsage
          usage={paneSession?.usage}
          sessionId={agentPane.sessionId}
          pinned={pinnedUsagePopover === agentPane.sessionId}
          onPopoverClosed={dismissUsagePopover}
        />
      </FitPiece>
      <FitPiece piece="provenance" levels={levels} fitLevels={['full', 'icon']}>
        <SessionProvenance
          automation={paneSession?.automation}
          pullRequests={paneSession?.pullRequests}
          interactive
          popoverGroup={{
            id: `${agentPane.id}:details`,
            activeId: provenancePopoverOwner,
            onOpen: setProvenancePopoverOwner,
          }}
        />
      </FitPiece>
      <span className="desktop-pane-harness" data-label={harnessLabel(paneSession?.agent)} aria-hidden="true" />
    </div>
  );
}
