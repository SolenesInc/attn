import { formatSessionAgentLabel, normalizeSessionAgent } from '../types/sessionAgent';

export function harnessLabel(agent?: string): string {
  const name = normalizeSessionAgent(agent, '');
  return name ? formatSessionAgentLabel(name) : 'Unknown harness';
}
