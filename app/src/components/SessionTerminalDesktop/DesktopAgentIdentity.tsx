import { DelegationChainTrigger } from '../DelegationChain';
import { SessionProvenance } from '../SessionProvenance';
import { HeaderSessionUsage } from './SessionUsage';
import { useDesktopContext } from './DesktopContext';
import type { DesktopAgentProps } from './desktopTypes';
export function DesktopAgentIdentity({ agentPane, paneSession, paneTitle }: DesktopAgentProps) {
  const {
    pinnedUsagePopover,
    dismissUsagePopover,
    provenancePopoverOwner,
    setProvenancePopoverOwner,
    delegationSessionById,
    delegatesByDispatcherId,
  } = useDesktopContext();
  const delegationSession = delegationSessionById.get(agentPane.sessionId);
  const delegates = delegatesByDispatcherId.get(agentPane.sessionId) ?? [];

  return (
    <>
      {' '}
      <span className="desktop-pane-identity">
        <span className="desktop-pane-identity-main">
          <span className="desktop-pane-title">{paneTitle}</span>
          <HeaderSessionUsage
            usage={paneSession?.usage}
            sessionId={agentPane.sessionId}
            pinned={pinnedUsagePopover === agentPane.sessionId}
            onPopoverClosed={dismissUsagePopover}
          />
          {delegationSession && (
            <DelegationChainTrigger
              session={delegationSession}
              hasDelegates={delegates.length > 0}
              variant="header"
              onOpen={() => setProvenancePopoverOwner(`${agentPane.id}:delegation`)}
            />
          )}
        </span>
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
      </span>
    </>
  );
}
