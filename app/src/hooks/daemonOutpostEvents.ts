import type { OutpostInfo } from '../types/generated';

type OutpostCommand = 'add_outpost' | 'repair_outpost' | 'remove_outpost' | 'update_outpost';

export type OutpostCommandMessage =
  | { cmd: 'add_outpost'; name: string; address: string }
  | { cmd: 'repair_outpost' | 'remove_outpost' | 'update_outpost'; id: string };

export function isOutpostCommand(command: unknown): command is OutpostCommand {
  return command === 'update_outpost' || command === 'add_outpost' || command === 'repair_outpost' || command === 'remove_outpost';
}

export function handleOutpostDaemonEvent(
  event: { event?: string; cmd?: string; error?: string; outposts?: OutpostInfo[] },
  setOutposts: (rows: OutpostInfo[]) => void,
  setError: (error: string) => void,
): boolean {
  if (event.event === 'outposts_updated') {
    setOutposts(event.outposts ?? []);
    return true;
  }
  if (event.event === 'command_error' && isOutpostCommand(event.cmd)) {
    setError(event.error === 'daemon_recovering' ? 'The home daemon is recovering. Try again when it is ready.' : event.error || `Could not ${event.cmd.replace('_', ' ')}`);
    return true;
  }
  return false;
}
