import { fireEvent, screen, within } from '@testing-library/react';
import { expect, it } from 'vitest';
import { openSection } from '../test/settings';
import { gesture, pressShortcut } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';

it('shows a paired sender\'s latest use when Settings reopens', async () => {
  let lastUsed: string | undefined;
  const daemon = await openSection('senders', {}, (d) => {
    d.on('senders_get', () => ({
      event: 'senders_updated',
      senders: [{ id: 'sender-1', name: 'capture', created_at: '2026-11-01T09:00:00Z', last_used_at: lastUsed }],
    } satisfies EventMessage<'senders_updated'>));
  });
  const row = () => within(screen.getByRole('region', { name: 'Paired senders' })).getByRole('listitem', { name: 'capture' });
  expect(row()).toHaveTextContent('Never used');

  lastUsed = '2026-11-02T15:00:00Z';
  await gesture(daemon, () => pressShortcut('ui.openSettings'));
  await gesture(daemon, () => {
    pressShortcut('ui.openSettings');
    fireEvent.click(screen.getByTestId('settings-nav-senders'));
  });
  expect(row()).toHaveTextContent('Last used');
});
