import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp } from '../test/renderApp';
import type { Reply } from '../test/scriptedDaemon';
import { renderSettings } from '../test/settings';

const autoModeState = (proposals: number): Reply => ({
  event: 'automode_state_result',
  success: true,
  config: {
    enabled_default: true,
    approval_policy: 'on-request',
    sandbox_mode: 'workspace-write',
    environment: { slots: [], notes: [] },
    rules: [],
    shipped_rules: [],
    network: { enabled: true, allowed_domains: [], denied_domains: [], allow_local_binding: false },
    shipped_denied_domains: [],
    legacy_patterns: [],
    presets: [],
  },
  proposals: Array.from({ length: proposals }, (_unused, index) => ({
    id: index + 1,
    kind: 'rule',
    target: '',
    value: `{"pattern":["curl","https://example.com/${index}"],"decision":"allow"}`,
    summary: `allow curl https://example.com/${index}`,
    proposed_by: { ref: 'session:session-a', name: 'session-a' },
    state: 'pending',
    created_at: '2026-08-16T10:00:00Z',
    resolved_at: '',
  })),
  denials: [],
  environment_slots: [],
});

const settingsWithProposals = (proposals: number) =>
  renderSettings({}, (daemon) => daemon.on('automode_get', () => autoModeState(proposals)));

describe('SettingsModal auto mode navigation', () => {
  it('opens pending proposals from a plain navigation label', async () => {
    const { daemon } = await settingsWithProposals(3);

    const nav = screen.getByRole('button', { name: /^Auto mode$/ });
    expect(daemon.sentOf('automode_get')).toHaveLength(1);
    expect(screen.queryByTestId('settings-automode-config')).toBeNull();

    await gesture(daemon, () => fireEvent.click(nav));
    expect(screen.getByTestId('automode-proposals')).toBeInTheDocument();
    expect(screen.getByTestId('settings-section-autoMode')).toBeInTheDocument();
    expect(screen.getByText('3 proposals waiting')).toBeInTheDocument();
  });

  it('reads nothing while settings is closed', async () => {
    const { daemon } = await renderApp();
    daemon.on('automode_get', () => autoModeState(2));
    await daemon.idle();

    expect(daemon.sentOf('automode_get')).toEqual([]);
  });
});
