import { fireEvent, screen, within } from '@testing-library/react';
import { openPath } from '@tauri-apps/plugin-opener';
import { describe, expect, it, vi } from 'vitest';
import type { Vent } from '../types/generated';
import { daemonSeed, daemonSession, DEFAULT_PROFILE_ID } from '../test/daemonFixtures';
import { gesture } from '../test/renderApp';
import type { ScriptedDaemon } from '../test/scriptedDaemon';
import { openSection } from '../test/settings';

function vent(id: string, overrides: Partial<Vent> = {}): Vent {
  return {
    id,
    session_id: 's1',
    text: '`attn seed tend s-f68sja` refused: held by a session that ended two hours ago.',
    created_at: '2026-10-24T14:02:11Z',
    context: {
      session_label: 'fix garden bell staleness',
      harness: 'claude',
      harness_version: '2.4.1',
      model: 'opus-5.5',
      seed_id: 's-stake1',
      build: { version: '0.42.1', commit: 'a3f9e1c22', channel: 'source', dirty: true, build_time: '', fingerprint: '', protocol: '', os: 'darwin', arch: 'arm64', app_version: '', app_channel: '' },
      experimental: ['asks_enabled'],
      transcript: { path: '/tmp/transcript.jsonl', bytes: 4096 },
    },
    ...overrides,
  };
}

const VENTS = [
  vent('v-new'),
  vent('v-gone', { text: 'kubectl cannot reach the cluster.', withdrawn_at: '2026-10-24T14:05:00Z', context: { ...vent('x').context, session_label: 'songcheck deploy' } }),
];

function serveVents(daemon: ScriptedDaemon) {
  let vents = VENTS;
  daemon.on('set_vent_dismissed', ({ vent_id, dismissed }) => {
    vents = vents.map((v) => (v.id === vent_id ? { ...v, dismissed_at: dismissed ? '2026-10-24T14:10:00Z' : undefined } : v));
    return [
      { event: 'set_vent_dismissed_result', success: true, vent_id },
      { event: 'vents_updated', profile_id: DEFAULT_PROFILE_ID, vents },
    ];
  });
  daemon.on('seed_plant', () => ({ event: 'seed_plant_result', success: true, seed: daemonSeed('s-planted', { step_slug: 'seed-tend-refused-held-session' }) }));
  daemon.on('seed_transition', ({ seed_id }) => ({ event: 'seed_transition_result', success: true, seed: daemonSeed(seed_id, { status: 'withdrawn' }) }));
}

async function openVents() {
  return openSection('analytics', {
    settings: { 'analytics.enabled': 'true' },
    sessions: [daemonSession('s1')],
    seeds: [daemonSeed('s-stake1', { step_slug: 'garden-show-stale-bell' })],
    vents: VENTS,
  }, serveVents);
}

const page = () => screen.getByTestId('settings-analytics');
const press = (daemon: ScriptedDaemon, key: string) => gesture(daemon, () => fireEvent.keyDown(page(), { key }));
const inspector = () => within(screen.getByTestId('analytics-inspector'));

describe('Settings › Analytics vents', () => {
  it('lists new vents before done ones and shows the selected vent with its context', async () => {
    await openVents();
    const rows = within(screen.getByRole('list', { name: 'Vents' })).getAllByRole('listitem').map((row) => row.textContent);
    expect(rows[0]).toMatch(/^New.*fix garden bell staleness/);
    expect(rows[1]).toMatch(/^Done.*songcheck deploy.*withdrawn/);
    expect(inspector().getByText('garden-show-stale-bell')).toBeInTheDocument();
    expect(inspector().getByText('Source a3f9e1c, uncommitted changes')).toBeInTheDocument();
    expect(inspector().getByText('Asks')).toBeInTheDocument();
    expect(inspector().getByText(/Claude 2\.4\.1/)).toBeInTheDocument();
    expect(inspector().getByText('Kept on this machine')).toBeInTheDocument();
  });

  it('dismisses with d, undoes with u, and keeps the withdrawn vent out of reach', async () => {
    const daemon = await openVents();
    await press(daemon, 'd');
    expect(screen.getByRole('status')).toHaveTextContent('Dismissed “fix garden bell staleness”.');
    expect(inspector().getByText('Dismissed')).toBeInTheDocument();
    await press(daemon, 'u');
    await press(daemon, 'j');
    await press(daemon, 'd');
    expect(daemon.sentOf('set_vent_dismissed').map(({ vent_id, dismissed }) => [vent_id, dismissed])).toEqual([
      ['v-new', true],
      ['v-new', false],
    ]);
    expect(inspector().queryByRole('button', { name: /Dismiss|Restore/ })).toBeNull();
  });

  it('plants a seed from the vent with p and withers it on undo', async () => {
    const daemon = await openVents();
    await press(daemon, 'p');
    expect(daemon.sentOf('seed_plant')).toEqual([expect.objectContaining({
      title: '`attn seed tend s-f68sja` refused: held by a session that ended two hours ago.',
      body: expect.stringMatching(/^`attn seed tend s-f68sja` refused.*\n\nFrom vent v-new by fix garden bell staleness \(Claude · opus-5\.5\)/s),
    })]);
    expect(screen.getByRole('status')).toHaveTextContent('Planted seed-tend-refused-held-session from “fix garden bell staleness”.');
    await press(daemon, 'u');
    expect(daemon.sentOf('seed_transition')).toEqual([expect.objectContaining({ seed_id: 's-planted', verb: 'wither' })]);
  });

  it('opens the transcript with t and the stake seed with g, closing settings', async () => {
    const daemon = await openVents();
    await press(daemon, 't');
    expect(vi.mocked(openPath)).toHaveBeenCalledWith('/tmp/transcript.jsonl');
    await press(daemon, 'g');
    expect(screen.queryByTestId('settings-modal')).toBeNull();
  });

  it('updates live when a vent arrives', async () => {
    const daemon = await openVents();
    await gesture(daemon, () => daemon.emit({ event: 'vents_updated', profile_id: DEFAULT_PROFILE_ID, vents: [vent('v-newer', { text: 'make test ran the Go suite for an app-only change.' }), ...VENTS] }));
    expect(within(screen.getByRole('list', { name: 'Vents' })).getAllByRole('listitem')[0]).toHaveTextContent('make test ran the Go suite');
    expect(daemon.sentOf('list_analytics_records')).toEqual([]);
  });
});
