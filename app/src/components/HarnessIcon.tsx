import { formatSessionAgentLabel, normalizeSessionAgent } from '../types/sessionAgent';
import { ClaudeIcon } from './icons/ClaudeIcon';
import { CodexIcon } from './icons/CodexIcon';
import { CopilotIcon } from './icons/CopilotIcon';
import { PiIcon } from './icons/PiIcon';

export function harnessLabel(agent?: string): string {
  const name = normalizeSessionAgent(agent, '');
  return name ? formatSessionAgentLabel(name) : 'Unknown harness';
}

export function HarnessIcon({ agent }: { agent?: string }) {
  const name = normalizeSessionAgent(agent, '');
  const label = harnessLabel(agent);
  let icon;
  switch (name) {
    case 'claude':
      icon = <ClaudeIcon />;
      break;
    case 'codex':
      icon = <CodexIcon />;
      break;
    case 'pi':
      icon = <PiIcon />;
      break;
    case 'copilot':
      icon = <CopilotIcon />;
      break;
    default:
      icon = (
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
          <rect x="3" y="4" width="18" height="16" rx="3" />
          {name === 'shell' ? (
            <path d="m7 9 3 3-3 3m6 0h4" />
          ) : (
            <path d="M8 10v2m8-2v2m-7 4h6" />
          )}
        </svg>
      );
  }
  return (
    <span className="sidebar-harness-icon" role="img" aria-label={label} title={label}>
      {icon}
    </span>
  );
}
