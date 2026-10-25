import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { VentSend, type Vent } from '../../types/generated';
import { daemonSession, DEFAULT_PROFILE_ID } from '../../test/daemonFixtures';
import { gesture } from '../../test/renderApp';
import type { ScriptedDaemon } from '../../test/scriptedDaemon';
import { openSection, savedSettings } from '../../test/settings';

const REFUSAL = 'attn-ingest refused the batch: events_per_install_per_minute is 10000, and this batch would make it 10200; retrying in 41s';

const VENT: Vent = {
  id: 'v-new',
  session_id: 's1',
  text: '`attn seed tend s-f68sja` refused: held by a session that ended two hours ago.',
  created_at: '2026-10-24T14:02:11Z',
  context: {
    session_label: 'fix garden bell staleness',
    harness: 'claude',
    build: { version: '0.42.1', commit: 'a3f9e1c22', channel: 'source', dirty: false, build_time: '', fingerprint: '', protocol: '', os: 'darwin', arch: 'arm64', app_version: '', app_channel: '' },
    experimental: [],
  },
};

function serveSharing(daemon: ScriptedDaemon, status: { last_refusal?: string }, initial: Vent[]) {
  let vents = initial;
  daemon.on('get_analytics_status', () => ({
    event: 'get_analytics_status_result', success: true, records: 40, bytes: 4096, waiting: 12, last_sent_at: '2026-10-24T14:00:12Z', ...status,
  }));
  daemon.on('set_vent_send', ({ vent_id, send }) => {
    vents = vents.map((v) => (v.id === vent_id ? { ...v, send: send ? VentSend.Queued : undefined } : v));
    return [
      { event: 'set_vent_send_result', success: true, vent_id },
      { event: 'vents_updated', profile_id: DEFAULT_PROFILE_ID, vents },
    ];
  });
  daemon.on('forget_analytics_install', () => ({ event: 'forget_analytics_install_result', success: true }));
}

async function openAnalytics(settings: Record<string, string>, status: { last_refusal?: string } = {}, vents: Vent[] = [VENT]) {
  return openSection('analytics', {
    settings: { 'analytics.enabled': 'true', ...settings },
    sessions: [daemonSession('s1')],
    vents,
  }, (daemon) => serveSharing(daemon, status, vents));
}

const page = () => screen.getByTestId('settings-analytics');
const press = (daemon: ScriptedDaemon, key: string) => gesture(daemon, () => fireEvent.keyDown(page(), { key }));
const click = (daemon: ScriptedDaemon, name: string) => gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
const inspector = () => within(screen.getByTestId('analytics-inspector'));

describe('Settings › Analytics sharing', () => {
  it('keeps every vent here while sharing is off, and x does nothing', async () => {
    const daemon = await openAnalytics({});
    expect(page()).toHaveTextContent('SharingOff');
    expect(inspector().getByText('Kept on this machine')).toBeInTheDocument();
    expect(inspector().getByText('· sharing is off')).toBeInTheDocument();
    await press(daemon, 'x');
    expect(daemon.sentOf('set_vent_send')).toEqual([]);
  });

  it('says a vent queued earlier waits for sharing, and x keeps it here', async () => {
    const daemon = await openAnalytics({ 'analytics.share_vents': 'true' }, {}, [{ ...VENT, send: VentSend.Queued }]);
    expect(inspector().getByText('Sending once sharing is on')).toBeInTheDocument();
    await press(daemon, 'x');
    expect(daemon.sentOf('set_vent_send').map(({ send }) => send)).toEqual([false]);
    expect(inspector().getByText('Kept on this machine')).toBeInTheDocument();
    await press(daemon, 'm');
    expect(screen.getByTestId('analytics-machine')).toHaveTextContent('New vents are sent with their text.');
  });

  it('sends a vent with x, keeps it with x again, and undoes with u', async () => {
    const daemon = await openAnalytics({ 'analytics.sharing_enabled': 'true' });
    expect(page()).toHaveTextContent('SharingOn · sent');
    await press(daemon, 'x');
    expect(screen.getByRole('status')).toHaveTextContent('Sending “fix garden bell staleness” with the next batch.');
    expect(inspector().getByText('Sending with the next batch')).toBeInTheDocument();
    await press(daemon, 'x');
    expect(inspector().getByText('Kept on this machine')).toBeInTheDocument();
    await press(daemon, 'u');
    expect(daemon.sentOf('set_vent_send').map(({ send }) => send)).toEqual([true, false, true]);
  });

  it('shows a refusal with its limit on the strip and in the Share row', async () => {
    const daemon = await openAnalytics({ 'analytics.sharing_enabled': 'true' }, { last_refusal: REFUSAL });
    expect(page()).toHaveTextContent('SharingRefused · 12 waiting');
    expect(page()).toHaveTextContent(REFUSAL);
    await press(daemon, 'm');
    expect(screen.getByTestId('analytics-machine')).toHaveTextContent(`Share with attn's maintainers${REFUSAL}`);
  });

  it('offers deleting the server copy when sharing is turned off', async () => {
    const daemon = await openAnalytics({ 'analytics.sharing_enabled': 'true', 'analytics.share_vents': 'true' });
    await press(daemon, 'm');
    expect(screen.getByTestId('analytics-machine')).toHaveTextContent('Last sent');
    expect(screen.getByTestId('analytics-machine')).toHaveTextContent('New vents are sent with their text.');
    const share = screen.getByText("Share with attn's maintainers").closest('.settings-row-card') as HTMLElement;
    await gesture(daemon, () => fireEvent.click(within(share).getByRole('button', { name: 'Disable' })));
    expect(savedSettings(daemon)).toEqual([['analytics.sharing_enabled', 'false']]);
    expect(share).toHaveTextContent('Sharing is off. Delete what the server already holds?');
    await click(daemon, 'Delete from server');
    expect(daemon.sentOf('forget_analytics_install')).toHaveLength(1);
    expect(screen.getByRole('status')).toHaveTextContent('Forgot this install.');
  });

  it('forgets this install after a confirmation', async () => {
    const daemon = await openAnalytics({ 'analytics.sharing_enabled': 'true' });
    await press(daemon, 'm');
    await click(daemon, 'Forget…');
    expect(daemon.sentOf('forget_analytics_install')).toEqual([]);
    await click(daemon, 'Delete from server');
    expect(daemon.sentOf('forget_analytics_install')).toHaveLength(1);
  });
});
