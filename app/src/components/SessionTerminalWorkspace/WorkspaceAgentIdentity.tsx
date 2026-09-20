import { DelegationChainTrigger } from '../DelegationChain';
import { SessionProvenance } from '../SessionProvenance';
import { HeaderSessionUsage } from './SessionUsage';
import { useWorkspaceContext } from './WorkspaceContext';
import type { WorkspaceAgentProps } from './workspaceTypes';
export function WorkspaceAgentIdentity({ agentPane, paneSession, paneTitle }: WorkspaceAgentProps) {
  const {
    pinnedUsagePopover,
    dismissUsagePopover,
    provenancePopoverOwner,
    setProvenancePopoverOwner,
    delegationSessionById,
    delegatesByDispatcherId,
  } = useWorkspaceContext();
  const delegationSession = delegationSessionById.get(agentPane.sessionId);
  const delegates = delegatesByDispatcherId.get(agentPane.sessionId) ?? [];

  return (
    <>
      {' '}
      <span className="workspace-pane-identity">
        <span className="workspace-pane-identity-main">
          <span className="workspace-pane-title">{paneTitle}</span>
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
		  sessionId={agentPane.sessionId}
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
