import type { SessionAgent } from '../types/sessionAgent';
import {
  agentLabel,
  type AgentAvailability,
  checkingForHarnessLabel,
  harnessAvailability,
} from '../utils/agentAvailability';
import './HarnessCheckingStatus.css';

export function HarnessCheckingStatus({
  agents,
  availability,
}: {
  agents: readonly SessionAgent[];
  availability: AgentAvailability;
}) {
  const checking = agents
    .filter((agent) => harnessAvailability(availability, agent) === 'checking')
    .map(agentLabel);
  return checking.length > 0 ? (
    <span className="harness-checking-status" role="status">
      {checkingForHarnessLabel(checking.join(' and '))}
    </span>
  ) : (
    <span className="harness-checking-status" />
  );
}
