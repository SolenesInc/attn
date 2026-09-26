import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

function usage(totalTokens: number): NonNullable<DaemonSession['usage']> {
  return {
    total_tokens: totalTokens,
    cost_usd: 0.5,
    has_unpriced_usage: false,
    models: [{
      model: 'claude-opus-5',
      purpose: 'agent',
      input_tokens: totalTokens,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_5m_tokens: 0,
      cache_write_1h_tokens: 0,
      cache_write_unclassified_tokens: 0,
      total_tokens: totalTokens,
      cost_usd: 0.5,
      has_unpriced_usage: false,
      unpriced_reason: '',
    }],
  };
}

async function showUsageFromActionMenu(daemon: ScriptedDaemon, label: string) {
  await gesture(daemon, () => pressShortcut('ui.actionMenu'));
  await gesture(daemon, () => fireEvent.click(screen.getByText(`Show ${label}'s usage`)));
}

const usageBreakdown = () => screen.queryByRole('dialog', { name: 'Session usage breakdown' });

describe('App pane header', () => {
  it('reopens a dismissed usage breakdown on request, and opens another session’s on its own request', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { state: 'idle', usage: usage(1_111) }), daemonSession('s2', { state: 'idle', usage: usage(2_222) })],
        workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
      },
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));

    await showUsageFromActionMenu(daemon, 's1');
    expect(usageBreakdown()).toHaveTextContent('1,111 tokens');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(usageBreakdown()).toBeNull();

    await showUsageFromActionMenu(daemon, 's1');
    expect(usageBreakdown()).toHaveTextContent('1,111 tokens');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await showUsageFromActionMenu(daemon, 's2');
    expect(usageBreakdown()).toHaveTextContent('2,222 tokens');
  });
});
