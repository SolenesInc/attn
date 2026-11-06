import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { gesture, renderApp, restartApp } from './test/renderApp';
import { openSection } from './test/settings';
import type { EventMessage } from './test/protocol';

type Outpost = EventMessage<'outposts_updated'>['outposts'][number];
const homeBuild = '0.13.0 (source git:home, protocol current)';
const old: Outpost = { id: 'studio', name: 'Studio', destination: 'studio:7777', state: 'refused', reach: 'unreachable', state_since: '2026-10-26T12:00:00Z', build: '0.13.0 (source git:old, protocol old)' };
const initial = { home_build: homeBuild, settings: { outposts_enabled: 'true' }, outposts: [old] };
const banner = () => screen.queryByRole('region', { name: 'Outpost updates' });

const done: Outpost = { ...old, build: homeBuild, state: 'connected', reach: 'reachable', update: { phase: 'done' } };
const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));

describe('outpost build updates', () => {
  afterEach(() => { vi.useRealTimers(); });

  it('updates every differing outpost once and shows phases and outcome', async () => {
    const view = await renderApp({ initialState: { ...initial, outposts: [old, { ...old, id: 'current', name: 'Current', build: homeBuild }] } });
    const daemon = view.daemon;
    daemon.on('update_outpost', () => {});
    expect(banner()).toHaveTextContent('Studio runs a different build than home.');
    await gesture(daemon, () => fireEvent.click(within(banner()!).getByRole('button', { name: 'Update outposts' })));
    expect(daemon.sentOf('update_outpost')).toEqual([{ cmd: 'update_outpost', id: 'studio' }]);
    for (const [phase, text] of [['building', 'Building attn for Linux.'], ['sending', 'Copying it over.'], ['verifying', 'Checking it runs.'], ['restarting', 'Restarting.']] as const) {
      await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase } }] }));
      expect(banner()).toHaveTextContent(`Updating outposts. Studio: ${text}`);
      expect(within(banner()!).getByRole('button', { name: 'Updating…' })).toBeDisabled();
    }
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, build: homeBuild, state: 'connected', reach: 'reachable', update: { phase: 'done' } }] }));
    expect(banner()).toHaveTextContent('Updated Studio.');
    expect(within(banner()!).queryByRole('button', { name: 'Update outposts' })).toBeNull();
    expect(daemon.sentOf('update_outpost')).toHaveLength(1);
  });

  it('shows the update started on the first click and sends no second update from repeated clicks', async () => {
    const view = await renderApp({ initialState: initial });
    view.daemon.on('update_outpost', () => ({ event: 'outposts_updated', outposts: [{ ...old, update: { phase: 'connecting' } }] }));
    await gesture(view.daemon, () => fireEvent.click(within(banner()!).getByRole('button', { name: 'Update outposts' })));
    expect(banner()).toHaveTextContent('Updating outposts. Studio: Connecting.');
    const button = within(banner()!).getByRole('button', { name: 'Updating…' });
    expect(button).toBeDisabled();
    await gesture(view.daemon, () => { fireEvent.click(button); fireEvent.click(button); });
    expect(view.daemon.sentOf('update_outpost')).toHaveLength(1);
  });

  it('says so in the banner when the daemon refuses the update', async () => {
    const view = await renderApp({ initialState: initial });
    view.daemon.on('update_outpost', () => ({ event: 'command_error', cmd: 'update_outpost', success: false, error: 'outpost studio not found' }));
    await gesture(view.daemon, () => fireEvent.click(within(banner()!).getByRole('button', { name: 'Update outposts' })));
    expect(banner()).toHaveTextContent('Studio runs a different build than home. outpost studio not found');
    expect(within(banner()!).getByRole('button', { name: 'Update outposts' })).toBeEnabled();
  });

  it('keeps dismissal through app restarts until the home build changes', async () => {
    const view = await renderApp({ initialState: initial });
    await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dismiss outpost updates' })));
    expect(banner()).toBeNull();
    const restarted = await restartApp(view, { initialState: initial });
    expect(banner()).toBeNull();
    await gesture(restarted.daemon, () => restarted.daemon.emit({ event: 'initial_state', ...initial, home_build: '0.13.0 (source git:next, protocol current)' }));
    expect(banner()).toHaveTextContent('Studio runs a different build than home.');
  });

  it('updates from a row, disables it during the push and keeps a refusal readable', async () => {
    const daemon = await openSection('outposts', initial, scripted => scripted.on('update_outpost', () => {}));
    const row = within(screen.getByRole('listitem', { name: 'Studio' }));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Update to home build' })));
    expect(daemon.sentOf('update_outpost')).toEqual([{ cmd: 'update_outpost', id: 'studio' }]);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase: 'downloading' } }] }));
    expect(row.getByRole('status')).toHaveTextContent('Downloading');
    expect(row.getByRole('button', { name: 'Updating…' })).toBeDisabled();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase: 'failed', outcome: 'Outpost database needs a newer build.' } }] }));
    expect(row.getByRole('status')).toHaveTextContent('needs a newer build');
    expect(row.getByRole('button', { name: 'Update to home build' })).toBeEnabled();
    expect(daemon.sentOf('update_outpost')).toHaveLength(1);
  });

  it('offers both update paths after a failed first install with no known build', async () => {
    const missing: Outpost = { ...old, build: undefined, last_error: 'nothing installed', update: { phase: 'failed', outcome: 'upload failed' } };
    const daemon = await openSection('outposts', { ...initial, outposts: [missing] }, scripted => scripted.on('update_outpost', () => {}));
    const row = within(screen.getByRole('listitem', { name: 'Studio' }));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Update to home build' })));
    await gesture(daemon, () => fireEvent.click(within(banner()!).getByRole('button', { name: 'Update outposts' })));
    expect(daemon.sentOf('update_outpost')).toEqual([{ cmd: 'update_outpost', id: 'studio' }, { cmd: 'update_outpost', id: 'studio' }]);
  });

  it('says a first install is in progress while an outpost with no build is building', async () => {
    const view = await renderApp({ initialState: { ...initial, outposts: [{ ...old, build: undefined, state: 'connecting', update: { phase: 'building' } }] } });
    expect(banner()).toHaveTextContent('Updating outposts. Studio: Building attn for Linux.');
    await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, build: homeBuild, state: 'connected', reach: 'reachable', update: { phase: 'done' } }] }));
    expect(banner()).toHaveTextContent('Updated Studio.');
  });

  it('stays hidden with no known mismatch or with Outposts off', async () => {
    const view = await renderApp({ initialState: { ...initial, outposts: [{ ...old, build: homeBuild }] } });
    expect(banner()).toBeNull();
    await gesture(view.daemon, () => view.daemon.emit({ event: 'settings_updated', settings: { outposts_enabled: 'false' } }));
    await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [old] }));
    expect(banner()).toBeNull();
  });

  it('counts the success down, pauses while hovered and closes at zero until a new update', async () => {
    const view = await renderApp({ initialState: initial });
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [done] }));
    expect(banner()).toHaveTextContent('Updated Studio.Closing in 5s');
    await advance(2000);
    expect(banner()).toHaveTextContent('Closing in 3s');
    fireEvent.mouseEnter(banner()!);
    await advance(10_000);
    expect(banner()).toHaveTextContent('Closing in 3s');
    fireEvent.mouseLeave(banner()!);
    await advance(2000);
    expect(banner()).toHaveTextContent('Closing in 1s');
    await advance(1000);
    expect(banner()).toBeNull();
    const restarted = await restartApp(view, { initialState: { ...initial, outposts: [done] } });
    expect(banner()).toBeNull();
    await gesture(restarted.daemon, () => restarted.daemon.emit({ event: 'outposts_updated', outposts: [done, { ...done, id: 'lab', name: 'Lab' }] }));
    expect(banner()).toHaveTextContent('Updated Studio and Lab.Closing in 5s');
  });

  it('closes the success at once from its dismiss button and shows the next update of the same outpost', async () => {
    const view = await renderApp({ initialState: { ...initial, outposts: [done] } });
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    fireEvent.mouseEnter(banner()!);
    await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dismiss outpost updates' })));
    expect(banner()).toBeNull();
    await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [{ ...old, update: { phase: 'building' } }] }));
    expect(banner()).toHaveTextContent('Updating outposts.');
    await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [done] }));
    expect(banner()).toHaveTextContent('Updated Studio.Closing in 5s');
    await advance(5000);
    expect(banner()).toBeNull();
  });

  it('never closes an update in progress, a failure or a different build on its own', async () => {
    const view = await renderApp({ initialState: initial });
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const states: [Outpost, string][] = [
      [old, 'Studio runs a different build than home.'],
      [{ ...old, update: { phase: 'verifying' } }, 'Updating outposts. Studio: Checking it runs.'],
      [{ ...old, update: { phase: 'failed', outcome: 'ssh: connection refused' } }, 'Couldn’t update Studio. Studio: ssh: connection refused'],
    ];
    for (const [row, text] of states) {
      await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [row] }));
      await advance(60_000);
      expect(banner()).toHaveTextContent(text);
      expect(banner()).not.toHaveTextContent('Closing in');
    }
  });
});
