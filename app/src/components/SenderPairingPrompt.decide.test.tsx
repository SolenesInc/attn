import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture, renderApp } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';
import type { SenderPairing } from '../types/generated';

const deploy: SenderPairing = { id: 'pair-1', name: 'deploy-bot', code: '4821', requested_at: '2026-11-02T10:00:00Z' };
const backup: SenderPairing = { id: 'pair-2', name: 'backup-script', code: '0937', requested_at: '2026-11-02T10:05:00Z' };

function senders(requests: SenderPairing[]): EventMessage<'senders_updated'> {
  return { event: 'senders_updated', requests, senders: [] };
}

const prompt = () => screen.queryByRole('dialog', { name: /send messages as you/ });

describe('Sender pairing prompt', () => {
  it('asks about the oldest request with its code, focuses Deny, and sends Allow', async () => {
    const { daemon } = await renderApp({ script: (d) => {
      d.on('senders_get', () => senders([]));
      d.on('sender_pair_decide', () => [{ event: 'sender_pair_decide_result', success: true }, senders([backup])]);
    } });
    await daemon.idle();
    expect(prompt()).toBeNull();

    await gesture(daemon, () => daemon.emit(senders([backup, deploy])));
    await act(() => vi.advanceTimersByTimeAsync(0));
    const dialog = screen.getByRole('dialog', { name: 'Allow “deploy-bot” to send messages as you?' });
    expect(dialog).toHaveTextContent('This program will be able to send files and messages that agents receive as your own.');
    expect(dialog).toHaveTextContent('4821');
    expect(dialog).toHaveTextContent('Check that the program shows the same code.');
    expect(within(dialog).getByRole('button', { name: 'Deny' })).toHaveFocus();

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(prompt()).not.toBeNull();

    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Allow' })));
    expect(daemon.sentOf('sender_pair_decide').map(({ pairing_id, allow }) => [pairing_id, allow])).toEqual([['pair-1', true]]);
    expect(screen.getByRole('dialog', { name: 'Allow “backup-script” to send messages as you?' })).toHaveTextContent('0937');

    await gesture(daemon, () => daemon.emit(senders([])));
    expect(prompt()).toBeNull();
  });

  it('keeps the request open and shows why a decision failed', async () => {
    const { daemon } = await renderApp({ script: (d) => {
      d.on('senders_get', () => senders([deploy]));
      d.on('sender_pair_decide', () => ({ event: 'sender_pair_decide_result', success: false, error: 'pairing request pair-1 expired' }));
    } });
    await daemon.idle();
    const dialog = screen.getByRole('dialog', { name: 'Allow “deploy-bot” to send messages as you?' });
    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Deny' })));
    expect(daemon.sentOf('sender_pair_decide').map(({ pairing_id, allow }) => [pairing_id, allow])).toEqual([['pair-1', false]]);
    expect(within(dialog).getByRole('alert')).toHaveTextContent('pairing request pair-1 expired');
    expect(within(dialog).getByRole('button', { name: 'Allow' })).toBeEnabled();
  });

  it('learns of requests made while the app was disconnected', async () => {
    let pending: SenderPairing[] = [];
    const { daemon } = await renderApp({ script: (d) => d.on('senders_get', () => senders(pending)) });
    await daemon.idle();
    expect(prompt()).toBeNull();

    pending = [deploy];
    await daemon.reconnect();
    await daemon.idle();
    expect(screen.getByRole('dialog', { name: 'Allow “deploy-bot” to send messages as you?' })).toBeInTheDocument();
    expect(daemon.sentOf('senders_get')).toHaveLength(2);
  });
});
