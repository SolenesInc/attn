import { afterEach, describe, expect, it, vi } from 'vitest';
import { execFileSync } from 'node:child_process';
import {
  assertPaneVisibleContentPreserved,
  runShellCommandInPane,
  waitForFirstWorkspacePane,
  waitForPaneInputFocus,
  waitForPaneTextChange,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';

describe('runShellCommandInPane', () => {
  afterEach(() => vi.useRealTimers());

  it('ignores repeated input echoes until the shell prints the completion markers', async () => {
    vi.useFakeTimers();
    let script;
    let reads = 0;
    const client = { request: vi.fn(async (method, args) => {
      if (method === 'write_pane') { script = args.text; return {}; }
      reads += 1;
      const echo = `$ ${script}\nnotification\n$ ${script}\n`;
      const output = execFileSync('/bin/sh', ['-c', script], { encoding: 'utf8' });
      if (reads === 1) return { text: echo };
      const [begin, result] = output.trim().split('\n');
      if (reads === 2) return { text: `${echo}${begin}\n${result}\n` };
      return { text: echo + output.replace(/(.{7})/g, '$1\n') };
    }) };
    let settled = false;
    const pending = runShellCommandInPane(client, { paneId: 'pane' }, "printf 'is harvested'", 'is harvested');
    pending.then(() => { settled = true; });
    await vi.advanceTimersByTimeAsync(500);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(250);
    await expect(pending).resolves.toBe('is harvested');
    expect(reads).toBe(3);
  });

  it('does not accept an expected phrase that appears only in the echoed command', async () => {
    vi.useFakeTimers();
    let script;
    const client = { request: vi.fn(async (method, args) => {
      if (method === 'write_pane') { script = args.text; return {}; }
      return { text: `$ ${script}\n${execFileSync('/bin/sh', ['-c', script], { encoding: 'utf8' })}` };
    }) };
    const pending = runShellCommandInPane(client, {}, ": 'is harvested'; printf 'permission denied'", 'is harvested');
    const assertion = expect(pending).rejects.toThrow('permission denied');
    await vi.advanceTimersByTimeAsync(250);
    await assertion;
  });
});

describe('waitForPaneTextChange', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('resolves once pane text differs from the previous snapshot', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn()
        .mockResolvedValueOnce({ text: 'same' })
        .mockResolvedValueOnce({ text: 'same' })
        .mockResolvedValueOnce({ text: 'changed' }),
    };

    const pending = waitForPaneTextChange(client, 'session-1', 'pane-1', 'same', 'text change', 2_000);
    await vi.advanceTimersByTimeAsync(500);

    await expect(pending).resolves.toEqual({ text: 'changed' });
    expect(client.request).toHaveBeenCalledTimes(3);
  });

  it('times out when pane text never changes', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn().mockResolvedValue({ text: 'same' }),
    };

    const pending = waitForPaneTextChange(client, 'session-1', 'pane-1', 'same', 'text change', 600);
    const assertion = expect(pending).rejects.toThrow(/Timed out waiting for text change/);
    await vi.advanceTimersByTimeAsync(1_000);
    await assertion;
  });

  it('retries transient session absence while waiting for pane text change', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn()
        .mockRejectedValueOnce(new Error('Automation request failed: read_pane_text: Session not found'))
        .mockResolvedValueOnce({ text: 'same' })
        .mockResolvedValueOnce({ text: 'changed' }),
    };

    const pending = waitForPaneTextChange(client, 'session-1', 'pane-1', 'same', 'text change', 2_000);
    await vi.advanceTimersByTimeAsync(800);

    await expect(pending).resolves.toEqual({ text: 'changed' });
    expect(client.request).toHaveBeenCalledTimes(3);
  });
});

describe('waitForPaneInputFocus', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('can require focus to stay true across a stabilization delay', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn()
        .mockResolvedValueOnce({ inputFocused: true })
        .mockResolvedValueOnce({ inputFocused: true }),
    };

    const pending = waitForPaneInputFocus(client, 'session-1', 'pane-1', 2_000, { stableMs: 300 });
    await vi.advanceTimersByTimeAsync(400);

    await expect(pending).resolves.toEqual({ inputFocused: true });
    expect(client.request).toHaveBeenCalledTimes(2);
  });
});

describe('waitForPaneVisible', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('retries transient session absence while waiting for pane visibility', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn()
        .mockRejectedValueOnce(new Error('Automation request failed: get_pane_state: Session not found'))
        .mockResolvedValueOnce({
          pane: {
            bounds: { width: 160, height: 120 },
          },
          renderHealth: {
            flags: {
              terminalVisible: true,
            },
          },
        }),
    };

    const pending = waitForPaneVisible(client, 'session-1', 'pane-1', 2_000);
    await vi.advanceTimersByTimeAsync(500);

    await expect(pending).resolves.toMatchObject({
      pane: {
        bounds: { width: 160, height: 120 },
      },
    });
    expect(client.request).toHaveBeenCalledTimes(2);
  });
});

describe('waitForFirstWorkspacePane', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('resolves the first daemon-reported pane instead of assuming a fixed pane id', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn()
        .mockResolvedValueOnce({ panes: [] })
        .mockResolvedValueOnce({ panes: [{ paneId: 'session-pane-1', runtimeId: 'runtime-1' }] }),
    };

    const pending = waitForFirstWorkspacePane(client, 'session-1', 'first pane', 2_000);
    await vi.advanceTimersByTimeAsync(500);

    await expect(pending).resolves.toEqual({ paneId: 'session-pane-1', runtimeId: 'runtime-1' });
    expect(client.request).toHaveBeenCalledWith('get_workspace', { sessionId: 'session-1' }, { timeoutMs: 20_000 });
  });
});

describe('assertPaneVisibleContentPreserved', () => {
  it('allows healthy widen recovery when anchors fully match but wrapped line count drops', async () => {
    const baselineVisibleContent = {
      lines: [
        '╭─────────────────────────────────────',
        '───────╮',
        '│ >_ OpenAI Codex (v0.118.0)',
        '│ model:     gpt-5.4 high   /model to',
        'change │',
        '│ directory: ~',
        'extra wrapped line 1',
        'extra wrapped line 2',
      ],
      summary: {
        nonEmptyLineCount: 36,
        charCount: 845,
      },
    };

    const currentVisibleContent = {
      lines: [
        '',
        '╭────────────────────────────────────────────╮',
        '│ >_ OpenAI Codex (v0.118.0)                 │',
        '│                                            │',
        '│ model:     gpt-5.4 high   /model to change │',
        '│ directory: ~                               │',
        '╰────────────────────────────────────────────╯',
        '',
        'Tip: New 2x rate limits until April',
        'TR205 anchor payload line 1',
      ],
      summary: {
        nonEmptyLineCount: 24,
        charCount: 1008,
      },
    };

    const client = {
      request: vi.fn().mockResolvedValue({
        pane: {
          visibleContent: currentVisibleContent,
        },
      }),
    };

    await expect(assertPaneVisibleContentPreserved(
      client,
      'session-1',
      'pane-a',
      baselineVisibleContent,
      {
        minNonEmptyLineRatio: 0.7,
        minCharCountRatio: 0.55,
        minAnchorMatches: 2,
        timeoutMs: 100,
        description: 'widen recovery',
      },
    )).resolves.toMatchObject({
      matches: expect.arrayContaining([
        '│ >_ OpenAI Codex (v0.118.0)',
        '│ model:     gpt-5.4 high   /model to',
      ]),
    });
  });

  it('ignores volatile baseline lines when selecting preservation anchors', async () => {
    const baselineVisibleContent = {
      lines: [
        '• Starting MCP servers (0/4): Railway, codex_apps, playwright, … (0s • esc to interrupt)',
        '› Write tests for @filename',
        'gpt-5.4 high · 100% left · ~',
      ],
      summary: {
        nonEmptyLineCount: 3,
        charCount: 143,
      },
    };

    const currentVisibleContent = {
      lines: [
        '│                                            │',
        '│ model:     gpt-5.4 high   /model to change │',
        '│ directory: ~                               │',
        '╰────────────────────────────────────────────╯',
        '',
        '  Tip: New Use /fast to enable our fastest inference at 2X',
        ' plan usage.',
        '',
        '› Write tests for @filename',
        '',
        '  gpt-5.4 high · 100% left · ~',
      ],
      summary: {
        nonEmptyLineCount: 8,
        charCount: 306,
      },
    };

    const client = {
      request: vi.fn().mockResolvedValue({
        pane: {
          visibleContent: currentVisibleContent,
        },
      }),
    };

    await expect(assertPaneVisibleContentPreserved(
      client,
      'session-1',
      'pane-a',
      baselineVisibleContent,
      {
        minNonEmptyLineRatio: 0.5,
        minCharCountRatio: 0.4,
        minAnchorMatches: 2,
        ignoreAnchorPatterns: [
          /^• Starting MCP servers\b/,
          /^Tip:/,
        ],
        timeoutMs: 100,
        description: 'volatile codex header line ignored',
      },
    )).resolves.toMatchObject({
      anchors: [
        '› Write tests for @filename',
        'gpt-5.4 high · 100% left · ~',
      ],
      matches: [
        '› Write tests for @filename',
        'gpt-5.4 high · 100% left · ~',
      ],
    });
  });
});
