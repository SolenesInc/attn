import { screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import type { SessionLedgerPage, SessionLedgerQuery } from '../../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry } from '../../types/generated';
import { pressShortcut, renderApp } from '../../test/renderApp';
import type { CommandMessage } from '../../test/protocol';
import type { ScriptedDaemon, ScriptedDaemonOptions } from '../../test/scriptedDaemon';
import { DEFAULT_DESKTOP_ID, defaultProfile } from '../../test/daemonFixtures';
import { NOW } from '../../test/sessionLedgerFixtures';

export type LedgerAnswer = (query: SessionLedgerQuery, index: number) => SessionLedgerPage | Error | 'hold';

type SessionListCommand = CommandMessage<'session_list'>;

export function pages(answers: SessionLedgerPage[]): LedgerAnswer {
  return (_query, index) => answers[Math.min(index, answers.length - 1)];
}

export function page(overrides: Partial<SessionLedgerPage> = {}): SessionLedgerPage {
  return { entries: [], omitted: 0, ...overrides };
}

export const rows = () => within(screen.getByRole('listbox', { name: 'Rows' }));

function serveLedger(daemon: ScriptedDaemon, answer: LedgerAnswer) {
  const held: SessionListCommand[] = [];
  const queryOf = ({ cmd: _cmd, request_id: _requestId, ...query }: SessionListCommand) => query as SessionLedgerQuery;
  daemon.on('session_list', (command) => {
    const reply = answer(queryOf(command), daemon.sentOf('session_list').length - 1);
    if (reply === 'hold') {
      held.push(command);
      return;
    }
    return reply instanceof Error
      ? { event: 'session_list_result', success: false, error: reply.message }
      : { event: 'session_list_result', success: true, result: reply };
  });
  return {
    queries: () => daemon.sentOf('session_list').map(queryOf),
    release: async (index: number, result: SessionLedgerPage) => {
      const command = held[index];
      daemon.replyTo(command, { event: 'session_list_result', request_id: command.request_id, success: true, result });
      await daemon.idle();
    },
    closed: async (entry: SessionLedgerEntry) => {
      daemon.emit({ event: 'session_closed', session_ledger_entry: entry });
      await daemon.idle();
    },
  };
}

export async function openSessionsLedger(answer: LedgerAnswer, options: ScriptedDaemonOptions = {}) {
  const view = await renderApp(options);
  vi.setSystemTime(NOW);
  const ledger = serveLedger(view.daemon, answer);
  pressShortcut('sessions.open');
  await view.daemon.idle();
  return { ...view, ...ledger };
}

export function namedProfiles(names: Record<string, string>) {
  return [
    defaultProfile(DEFAULT_DESKTOP_ID),
    ...Object.entries(names).map(([id, name]) => defaultProfile('', { id, name })),
  ];
}
