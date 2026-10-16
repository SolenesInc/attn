import { harnessLabel } from '../components/harnessLabel';

export function sessionDisplayName(session: { label: string; title?: string; agent?: string }): string {
  return session.label || session.title || harnessLabel(session.agent);
}
