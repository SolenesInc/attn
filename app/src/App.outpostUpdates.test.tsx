import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp, restartApp } from './test/renderApp';
import { openSection } from './test/settings';
import type { EventMessage } from './test/protocol';

type Outpost = EventMessage<'outposts_updated'>['outposts'][number];
const homeBuild = '0.13.0 (source git:home, protocol current)';
const old: Outpost = { id: 'studio', name: 'Studio', destination: 'studio:7777', state: 'refused', reach: 'unreachable', state_since: '2026-10-26T12:00:00Z', build: '0.13.0 (source git:old, protocol old)' };
const initial = { home_build: homeBuild, settings: { outposts_enabled: 'true' }, outposts: [old] };
const banner = () => screen.queryByRole('region', { name: 'Outpost updates' });

describe('outpost build updates', () => {
  it('updates every differing outpost once and shows phases and outcome', async () => {
    const view = await renderApp({ initialState: { ...initial, outposts: [old, { ...old, id: 'current', name: 'Current', build: homeBuild }] } });
    const daemon = view.daemon;
    daemon.on('update_outpost', () => {});
    expect(banner()).toHaveTextContent('different build');
    await gesture(daemon, () => fireEvent.click(within(banner()!).getByRole('button', { name: 'Update outposts' })));
    expect(daemon.sentOf('update_outpost')).toEqual([{ cmd: 'update_outpost', id: 'studio' }]);
    for (const [phase, text] of [['building', 'Building'], ['sending', 'Sending'], ['verifying', 'Verifying'], ['restarting', 'Restarting']] as const) {
      await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase } }] }));
      expect(banner()).toHaveTextContent(text);
      expect(within(banner()!).getByRole('button', { name: 'Updating…' })).toBeDisabled();
    }
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, build: homeBuild, state: 'connected', reach: 'reachable', update: { phase: 'done', outcome: 'Updated to the home’s build.' } }] }));
    expect(banner()).toHaveTextContent('Updated to the home’s build.');
    expect(within(banner()!).queryByRole('button', { name: 'Update outposts' })).toBeNull();
    expect(daemon.sentOf('update_outpost')).toHaveLength(1);
  });

  it('keeps dismissal through app restarts until the home build changes', async () => {
    const view = await renderApp({ initialState: initial });
    await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dismiss outpost updates' })));
    expect(banner()).toBeNull();
    const restarted = await restartApp(view, { initialState: initial });
    expect(banner()).toBeNull();
    await gesture(restarted.daemon, () => restarted.daemon.emit({ event: 'initial_state', ...initial, home_build: '0.13.0 (source git:next, protocol current)' }));
    expect(banner()).toHaveTextContent('different build');
  });

  it('updates from a row, disables it during the push and keeps a refusal readable', async () => {
    const daemon = await openSection('outposts', initial, scripted => scripted.on('update_outpost', () => {}));
    const row = within(screen.getByRole('listitem', { name: 'Studio' }));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Update to home build' })));
    expect(daemon.sentOf('update_outpost')).toEqual([{ cmd: 'update_outpost', id: 'studio' }]);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase: 'downloading' } }] }));
    expect(row.getByRole('status')).toHaveTextContent('Downloading');
    expect(row.getByRole('button', { name: 'Updating…' })).toBeDisabled();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase: 'failed', outcome: 'Outpost is not running under systemd.' } }] }));
    expect(row.getByRole('status')).toHaveTextContent('not running under systemd');
    expect(row.getByRole('button', { name: 'Update to home build' })).toBeEnabled();
    expect(daemon.sentOf('update_outpost')).toHaveLength(1);
  });

  it('stays hidden with no known mismatch or with Outposts off', async () => {
    const view = await renderApp({ initialState: { ...initial, outposts: [{ ...old, build: homeBuild }] } });
    expect(banner()).toBeNull();
    await gesture(view.daemon, () => view.daemon.emit({ event: 'settings_updated', settings: { outposts_enabled: 'false' } }));
    await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [old] }));
    expect(banner()).toBeNull();
  });
});
