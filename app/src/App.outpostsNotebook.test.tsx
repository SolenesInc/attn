import { screen, within } from '@testing-library/react';
import { expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import { openSection } from './test/settings';
import type { EventMessage } from './test/protocol';
import capture from './test/fixtures/outposts-notebook.json';

it('shows Notebook health in the recorded daemon event order', async () => {
  const daemon = await openSection('outposts', { settings: { outposts_enabled: 'true' } });
  const states = new Set<string>();
  let progress = false;
  let conflict = false;
  let cleared = false;
  for (const event of capture.events as EventMessage<'outposts_updated'>[]) {
    await gesture(daemon, () => daemon.emit(event));
    const row = event.outposts.find(row => row.name === 'Studio');
    if (!row?.notebook) continue;
    const outpost = within(screen.getByRole('listitem', { name: 'Studio' }));
    const notebook = row.notebook;
    states.add(notebook.state);
    if (notebook.state === 'error') {
      expect(outpost.getByText(`Notebook sync error: ${notebook.error}`)).toBeInTheDocument();
    } else {
      expect(outpost.queryByText(/Notebook sync error:/)).toBeNull();
      const label = { first_sync: 'first sync', in_sync: 'in sync', syncing: 'syncing' }[notebook.state];
      expect(outpost.getByText(new RegExp(`^Notebook: ${label}`))).toBeInTheDocument();
      if ((notebook.need_items ?? 0) > 0 && (notebook.need_bytes ?? 0) > 0) {
        expect(outpost.getByText(/and \d+ items? left/)).toBeInTheDocument();
        progress = true;
      }
    }
    if (notebook.previous_root) expect(outpost.getByText(`Previous folder saved at ${notebook.previous_root}`)).toBeInTheDocument();
    if (notebook.conflicts === 1) {
      expect(outpost.getByText('1 conflict copy')).toBeInTheDocument();
      conflict = true;
    } else if (conflict) {
      expect(outpost.queryByText('1 conflict copy')).toBeNull();
      cleared = true;
    }
  }
  expect(states).toEqual(new Set(['first_sync', 'in_sync', 'syncing', 'error']));
  expect(progress && conflict && cleared).toBe(true);
  expect(daemon.sent.filter(message => message.cmd.includes('outpost'))).toEqual([]);
});
