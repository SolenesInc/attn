import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

type TerminalTextEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  text?: string;
};

export function handleTerminalTextDaemonEvent(event: TerminalTextEvent, pending: PendingRequests): boolean {
  if (event.event !== 'terminal_read_text_result') return false;
  settlePendingRequest(pending, 'terminal_read_text', event, (value) => value.text ?? '', 'Reading the terminal failed');
  return true;
}
