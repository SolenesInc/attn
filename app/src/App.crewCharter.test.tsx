import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, crewMember, daemonSession } from './test/daemonFixtures';
import type { CommandMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import { type Answer, answerInTurn, HOLD, type Reply, type ScriptedDaemon } from './test/scriptedDaemon';

const trellis = crewMember('trellis', { revision: 4 });

const charter = (content: string, token: string): Reply => ({ event: 'crew_charter_get_result', success: true, member: 'trellis', charter: { content, token } });
const charterRefused = (error: string): Reply => ({ event: 'crew_charter_get_result', success: false, error });
const saved = (content: string, token: string, conflict = false): Reply => ({ event: 'crew_charter_set_result', success: true, member: 'trellis', conflict, charter: { content, token } });
const refused = (error: string): Reply => ({ event: 'crew_charter_set_result', success: false, conflict: false, error });

async function openCharter({ reads = [charter('# One\n', 'one')], saves = [HOLD] }: { reads?: Answer[]; saves?: Answer[] } = {}) {
  const { daemon } = await renderApp({
    initialState: { crew: [trellis], sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] },
  });
  answerInTurn(daemon, 'crew_charter_get', reads);
  answerInTurn(daemon, 'crew_charter_set', saves);
  daemon.on('crew_handoffs_get', () => ({ event: 'crew_handoffs_get_result', success: true, member: 'trellis', handoffs: [] }));
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
  await click(daemon, 'Charter');
  return daemon;
}

const panel = () => within(screen.getByTestId('crew-panel'));
const editor = () => panel().getByLabelText('Charter for Trellis');
const status = () => panel().getByRole('status');
const charterSaves = (daemon: ScriptedDaemon) => daemon.sentOf('crew_charter_set').map(({ content, expected_token }) => ({ content, expected_token }));

async function click(daemon: ScriptedDaemon, name: string | RegExp) {
  await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name })));
}

async function type(daemon: ScriptedDaemon, content: string) {
  await gesture(daemon, () => fireEvent.change(editor(), { target: { value: content } }));
}

async function leaveField(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.blur(editor()));
}

async function wait(daemon: ScriptedDaemon, ms: number) {
  await act(() => vi.advanceTimersByTimeAsync(ms));
  await daemon.idle();
}

async function answer(daemon: ScriptedDaemon, command: CommandMessage<'crew_charter_get' | 'crew_charter_set'>, reply: Reply) {
  await gesture(daemon, () => daemon.replyTo(command, { ...reply, request_id: command.request_id } as Reply));
}

describe('App crew charter', () => {
  it('saves after a typing pause against the acknowledged version, and at once when the user leaves the field', async () => {
    const daemon = await openCharter();
    await type(daemon, '# Two\n');
    expect(status()).toHaveTextContent('Waiting to save');

    await wait(daemon, 699);
    expect(charterSaves(daemon)).toEqual([]);
    await wait(daemon, 1);
    expect(charterSaves(daemon)).toEqual([{ content: '# Two\n', expected_token: 'one' }]);
    expect(status()).toHaveTextContent('Saving…');

    await answer(daemon, daemon.sentOf('crew_charter_set')[0], saved('# Two\n', 'two'));
    expect(status()).toHaveTextContent('Saved');

    await type(daemon, '# Three\n');
    await leaveField(daemon);
    expect(charterSaves(daemon)[1]).toEqual({ content: '# Three\n', expected_token: 'two' });
    await wait(daemon, 700);
    expect(charterSaves(daemon)).toHaveLength(2);
  });

  it('sends an edit made while a save was out once it is acknowledged, and says Saved only after the last', async () => {
    const daemon = await openCharter();
    await type(daemon, 'B');
    await leaveField(daemon);
    await type(daemon, 'C');
    await leaveField(daemon);
    expect(charterSaves(daemon)).toEqual([{ content: 'B', expected_token: 'one' }]);

    await answer(daemon, daemon.sentOf('crew_charter_set')[0], saved('B', 'two'));
    expect(charterSaves(daemon)).toEqual([{ content: 'B', expected_token: 'one' }, { content: 'C', expected_token: 'two' }]);
    expect(status()).toHaveTextContent('Saving…');

    await answer(daemon, daemon.sentOf('crew_charter_set')[1], saved('C', 'three'));
    expect(status()).toHaveTextContent('Saved');
    expect(editor()).toHaveValue('C');
  });

  it('writes again after a save whose answer was lost, even once the text is back to what was saved', async () => {
    const daemon = await openCharter({ reads: [charter('A', 'a')] });
    await type(daemon, 'B');
    await leaveField(daemon);

    await wait(daemon, 10_000);
    expect(status()).toHaveTextContent('Not saved');
    expect(panel().getByRole('alert')).toHaveTextContent('Saving Trellis\'s charter timed out');

    await type(daemon, 'A');
    await leaveField(daemon);

    expect(charterSaves(daemon)).toEqual([{ content: 'B', expected_token: 'a' }, { content: 'A', expected_token: 'a' }]);
  });

  it('turns a reread that finds another version under an unsaved edit into a choice, and keeps the edit when asked', async () => {
    const daemon = await openCharter({
      reads: [charter('old', 'old-token'), charter('external', 'external-token')],
      saves: [refused('disk is read-only'), saved('mine', 'mine-token')],
    });
    await type(daemon, 'mine');
    await click(daemon, 'Launch settings');
    expect(panel().getByText('disk is read-only')).toBeInTheDocument();
    await click(daemon, 'Launch settings');

    await click(daemon, 'Charter');
    expect(panel().getByText('The file changed outside this editor.')).toBeInTheDocument();
    expect(editor()).toHaveValue('mine');

    await click(daemon, 'Keep my edit');

    expect(charterSaves(daemon)[1]).toEqual({ content: 'mine', expected_token: 'external-token' });
    expect(status()).toHaveTextContent('Saved');
  });

  it('retries a save that failed for want of a connection once it reconnects, but leaves other failures to the user', async () => {
    const daemon = await openCharter({ reads: [charter('old', 'old')], saves: [refused('disk is read-only')] });
    daemon.disconnect();
    await daemon.idle();
    await type(daemon, 'offline edit');
    await leaveField(daemon);
    expect(panel().getByRole('alert')).toHaveTextContent('WebSocket not connected');
    expect(charterSaves(daemon)).toEqual([]);

    await daemon.reconnect();
    await daemon.idle();
    expect(charterSaves(daemon)).toEqual([{ content: 'offline edit', expected_token: 'old' }]);
    expect(panel().getByRole('alert')).toHaveTextContent('disk is read-only');

    await daemon.reconnect();
    await daemon.idle();
    expect(charterSaves(daemon)).toHaveLength(1);
    expect(editor()).toHaveValue('offline edit');
  });

  it('keeps a write the daemon acknowledged when an older reread answers after it', async () => {
    const daemon = await openCharter({ reads: [charter('A', 'a'), HOLD], saves: [saved('B', 'b')] });
    await click(daemon, 'Launch settings');
    await click(daemon, 'Charter');
    const reread = daemon.sentOf('crew_charter_get')[1];

    await type(daemon, 'B');
    await leaveField(daemon);
    expect(status()).toHaveTextContent('Saved');

    await answer(daemon, reread, charter('A', 'a'));

    expect(editor()).toHaveValue('B');
    expect(status()).toHaveTextContent('Saved');
  });

  it('offers a retry when the charter cannot be read', async () => {
    const daemon = await openCharter({ reads: [charterRefused('charter unreadable'), charter('A', 'a')] });
    expect(panel().getByRole('alert')).toHaveTextContent('charter unreadable');

    await click(daemon, 'Retry');

    expect(editor()).toHaveValue('A');
    expect(status()).toHaveTextContent('Saved');
  });

  it('reads the charter again after a reconnect', async () => {
    const daemon = await openCharter({ reads: [charter('disk', 'disk-token'), charter('changed on disk', 'disk-token-2')] });
    expect(editor()).toHaveValue('disk');

    await daemon.reconnect();
    await daemon.idle();

    expect(editor()).toHaveValue('changed on disk');
  });
});
