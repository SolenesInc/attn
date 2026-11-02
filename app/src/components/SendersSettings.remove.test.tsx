import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSection } from '../test/settings';
import { gesture } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';
import type { SenderElement } from '../types/generated';

const deployBot: SenderElement = { id: 'sender-1', name: 'deploy-bot', created_at: '2026-11-01T09:00:00Z', last_used_at: '2026-11-02T10:00:00Z' };

function senders(paired: SenderElement[]): EventMessage<'senders_updated'> {
  return { event: 'senders_updated', requests: [], senders: paired };
}

const section = () => screen.getByRole('region', { name: 'Paired senders' });

describe('Settings → Paired senders', () => {
  it('lists a paired sender and removes it', async () => {
    const daemon = await openSection('senders', {}, (d) => {
      d.on('senders_get', () => senders([deployBot]));
      d.on('sender_unpair', () => [{ event: 'sender_unpair_result', success: true }, senders([])]);
    });
    const row = within(section()).getByRole('listitem', { name: 'deploy-bot' });
    expect(row).toHaveTextContent('Paired');
    expect(row).toHaveTextContent('Last used');

    await gesture(daemon, () => fireEvent.click(within(row).getByRole('button', { name: 'Remove' })));
    expect(daemon.sentOf('sender_unpair').map(({ sender }) => sender)).toEqual(['sender-1']);
    expect(within(section()).queryByRole('listitem', { name: 'deploy-bot' })).toBeNull();
    expect(section()).toHaveTextContent('No paired senders. A program pairs with attn send pair --name <name>.');
  });

  it('shows why removing a sender failed', async () => {
    const daemon = await openSection('senders', {}, (d) => {
      d.on('senders_get', () => senders([{ ...deployBot, last_used_at: undefined }]));
      d.on('sender_unpair', () => ({ event: 'sender_unpair_result', success: false, error: 'no paired sender named "deploy-bot"' }));
    });
    const row = within(section()).getByRole('listitem', { name: 'deploy-bot' });
    expect(row).toHaveTextContent('Never used');
    await gesture(daemon, () => fireEvent.click(within(row).getByRole('button', { name: 'Remove' })));
    expect(within(section()).getByRole('alert')).toHaveTextContent('no paired sender named "deploy-bot"');
  });
});
