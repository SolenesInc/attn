import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from '../test/protocol';
import { renderApp } from '../test/renderApp';

type Run = EventMessage<'update_changed'>['run'];

const run = (overrides: Partial<Run>): Run => ({
  id: 'run-1',
  target_build: 'v2',
  install_kind: 'source',
  phase: 'staging',
  log_path: '/tmp/run.log',
  requesters: [],
  outposts: [],
  ...overrides,
});

const banner = () => within(screen.getByRole('region', { name: 'attn update' }));

describe('update run banner with outposts', () => {
  it('shows each outpost while the run is open', async () => {
    const view = await renderApp();
    view.daemon.emit({ event: 'update_changed', run: run({ outposts: [{ id: 'a', name: 'sandbox', state: 'staging', detail: 'sending' }] }) });
    await view.daemon.idle();
    expect(banner().getByText(/sandbox: Copying it over\./)).toBeTruthy();
  });

  it('offers Update without it after a staging failure and leaves that outpost out', async () => {
    const view = await renderApp();
    const started: unknown[] = [];
    view.daemon.on('start_update', message => {
      started.push(message.without_outposts);
      return { event: 'start_update_result', success: true };
    });
    view.daemon.emit({ event: 'update_changed', run: run({ phase: 'failed', outcome: 'could not stage sandbox', outposts: [{ id: 'a', name: 'sandbox', state: 'failed', detail: 'denied' }] }) });
    await view.daemon.idle();
    fireEvent.click(banner().getByRole('button', { name: 'Update without sandbox' }));
    await view.daemon.idle();
    expect(started).toEqual([['a']]);
  });

  it('stays open with a skipped outpost after a finished run', async () => {
    const view = await renderApp();
    view.daemon.emit({ event: 'update_changed', run: run({ phase: 'done', outcome: 'attn is now running v2. Skipped sandbox.', outposts: [{ id: 'a', name: 'sandbox', state: 'skipped' }] }) });
    await view.daemon.idle();
    expect(banner().getByText(/Skipped sandbox/)).toBeTruthy();
    expect(banner().queryByText(/Closing in/)).toBeNull();
  });
});
