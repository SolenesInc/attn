import { describe, expect, it, vi } from 'vitest';
import { ensureCodexInitialPanePromptReady, mockAgentRepaintedToWidth } from './scenarioAgents.mjs';
import { mockAgentSplash } from './mockAgent.mjs';

describe('ensureCodexInitialPanePromptReady', () => {
  it('accepts the current Codex directory trust dialog before returning ready', async () => {
    let acceptedTrust = false;
    const client = {
      request: vi.fn(async (action) => {
        switch (action) {
          case 'get_workspace':
            return {
              panes: [{ paneId: 'pane-session-1', runtimeId: 'runtime-session-1' }],
            };
          case 'get_pane_state':
            return {
              pane: { bounds: { width: 640, height: 480 }, inputFocused: true },
              inputFocused: true,
              renderHealth: { flags: { terminalVisible: true } },
            };
          case 'read_pane_text':
            return {
              text: acceptedTrust
                ? '>_ OpenAI Codex\n100% left'
                : [
                    'Do you trust the contents of this directory?',
                    '1. Yes, continue',
                    '2. No, quit',
                    'Press enter to continue',
                  ].join('\n'),
            };
          case 'write_pane':
            acceptedTrust = true;
            return {};
          default:
            return {};
        }
      }),
    };

    await expect(ensureCodexInitialPanePromptReady(client, 'session-1', 2_000)).resolves.toMatchObject({
      trustHandled: true,
      text: expect.stringContaining('OpenAI Codex'),
    });
    expect(client.request).toHaveBeenCalledWith(
      'type_pane_via_ui',
      { sessionId: 'session-1', paneId: 'pane-session-1', text: '1' },
    );
    expect(client.request).toHaveBeenCalledWith(
      'write_pane',
      { sessionId: 'session-1', paneId: 'pane-session-1', text: '\r', submit: false },
    );
  });
});

describe('mockAgentRepaintedToWidth', () => {
  const cols = 75;
  const wrap = (line) => {
    const rows = [];
    for (let i = 0; i < line.length; i += cols) rows.push(line.slice(i, i + cols));
    return rows;
  };

  it('rejects the old wide splash reflowed into the narrower pane', () => {
    const wide = mockAgentSplash({ header: 'Claude Code mock agent', cwd: '/tmp/x', cols: 150 });
    const lines = wide.flatMap(wrap).concat(['', '• token line 1', '', '❯ typed prompt', '❯']);
    expect(mockAgentRepaintedToWidth({ cols, lines })).toBe(false);
  });

  it('accepts the splash repainted at the pane width', () => {
    const narrow = mockAgentSplash({ header: 'Claude Code mock agent', cwd: '/tmp/x', cols });
    const lines = ['', ...narrow, '', '• token line 1', '', '❯'];
    expect(mockAgentRepaintedToWidth({ cols, lines })).toBe(true);
  });

  it('rejects panes without geometry or content', () => {
    expect(mockAgentRepaintedToWidth(null)).toBe(false);
    expect(mockAgentRepaintedToWidth({ cols: 0, lines: ['╭╮'] })).toBe(false);
    expect(mockAgentRepaintedToWidth({ cols, lines: ['', '  '] })).toBe(false);
  });
});
