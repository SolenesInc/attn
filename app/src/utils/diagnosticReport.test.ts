import { describe, expect, it, vi } from 'vitest';
import type { DaemonSupportSnapshot } from '../hooks/useDaemonSocket';
import type { FrontendInputTraceSnapshot } from './supportInputTrace';
import {
  correlateNativeAndFrontendInput,
  createDiagnosticReport,
  deriveInputJourneys,
  beginDiagnosticCapture,
  sanitizeSupportDiagnostics,
  type NativeInputSnapshot,
  type PendingDiagnosticCapture,
} from './diagnosticReport';

function frontend(events: FrontendInputTraceSnapshot['events']): FrontendInputTraceSnapshot {
  return { capacity: 512, total: events.length, capturedAtUnixMs: 2_000, capturedAtMonotonicMs: 20, events };
}

function daemon(inputTraces: DaemonSupportSnapshot['input_traces']): DaemonSupportSnapshot {
  return {
    event: 'support_snapshot_result', request_id: 'request-1', protocol_version: '301',
    daemon_instance_id: 'daemon-1', daemon_started_at_unix_ms: 1,
    captured_at_unix_ms: 2_000, backend: 'embedded', warning_codes: [], trace_capacity: 512,
    trace_total: inputTraces.length, input_traces: inputTraces, runtimes: [],
  } as DaemonSupportSnapshot;
}

describe('diagnostic report evidence', () => {
  it('removes content-bearing and arbitrary error fields recursively', () => {
    const sanitized = sanitizeSupportDiagnostics({
      runtimeId: 'runtime-1',
      message: 'PRIVATE_MESSAGE',
      prompt: 'PRIVATE_PROMPT',
      clipboard: 'PRIVATE_CLIPBOARD',
      environment: { TOKEN: 'PRIVATE_TOKEN' },
      detail: {
        stack: 'PRIVATE_STACK', data: 'PRIVATE_DATA', modelFault: 'PRIVATE_MODEL_FAULT', runtimeId: 'hidden-runtime',
      },
      rows: [{ output: 'PRIVATE_OUTPUT', log: 'PRIVATE_LOG', durationMs: 4 }],
    });

    expect(sanitized).toEqual({ runtimeId: 'runtime-1', rows: [{ durationMs: 4 }] });
    expect(JSON.stringify(sanitized)).not.toContain('PRIVATE');
  });

  it('labels native correlation by evidence strength and preserves unmatched events', () => {
    const native: NativeInputSnapshot = {
      supported: true, os: 'macos', arch: 'aarch64', capacity: 512, total: 2, capturedAtUnixMs: 2_000,
      observations: [
        { sequence: 1, atUnixMs: 1_000, monotonicUs: 1, eventClass: 'key_down', modifiers: 8, repeat: false, windowNumber: 1, windowFocused: true, responderCategory: 'webview' },
        { sequence: 2, atUnixMs: 1_200, monotonicUs: 2, eventClass: 'key_down', modifiers: 0, repeat: false, windowNumber: 1, windowFocused: true, responderCategory: 'webview' },
      ],
    };
    const browser = frontend([
      { sequence: 1, traceId: 'trace-1', atUnixMs: 1_012, monotonicMs: 1, stage: 'document', event: 'keydown', modifiers: 8 },
      { sequence: 2, traceId: 'trace-2', atUnixMs: 1_500, monotonicMs: 2, stage: 'document', event: 'keydown', modifiers: 0 },
    ]);

    expect(correlateNativeAndFrontendInput(native, browser).matches).toEqual([
      { nativeSequence: 1, frontendTraceId: 'trace-1', deltaMs: 12, confidence: 'high', reason: 'time_order_and_modifiers' },
      { nativeSequence: 2, confidence: 'unmatched', reason: 'no_frontend_event_in_window' },
      { frontendTraceId: 'trace-2', confidence: 'unmatched', reason: 'no_native_event_in_window' },
    ]);
  });

  it('locates a traced input at the daemon write and next output boundary', () => {
    const browser = frontend([
      { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'document', event: 'keydown' },
      { sequence: 2, traceId: 'trace-1', atUnixMs: 1_001, monotonicMs: 2, stage: 'terminal', event: 'keydown', outcome: 'sent', runtimeId: 'runtime-1' },
      { sequence: 3, traceId: 'trace-1', atUnixMs: 1_002, monotonicMs: 3, stage: 'transport', event: 'websocket_send', runtimeId: 'runtime-1', socketState: 1, initialStateReceived: true },
    ]);
    const daemons = [daemon([{
      trace_id: 'trace-1', runtime_id: 'runtime-1', sequence: 1, received_at_unix_ms: 1_003,
      source: 'user', byte_count: 1, write_duration_us: 50, write_result: 'accepted',
    }])];
    const pty = { recentEvents: [{ at: new Date(1_010).toISOString(), kind: 'ws_event', event: 'pty_output', runtimeId: 'runtime-1' }] };

    expect(deriveInputJourneys(browser, daemons, pty)).toEqual([expect.objectContaining({
      traceId: 'trace-1', runtimeId: 'runtime-1', transportReady: true,
      daemonWrite: { result: 'accepted', durationUs: 50 },
      outputObservedAtUnixMs: 1_010,
      conclusion: 'pty_write_succeeded_output_received',
    })]);
  });

  it.each([
    {
      name: 'document to terminal boundary',
      events: [
        { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'document' as const, event: 'keydown' },
      ],
      daemons: [],
      conclusion: 'document_observed_no_terminal_decision',
    },
    {
      name: 'terminal to transport boundary',
      events: [
        { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'document' as const, event: 'keydown' },
        { sequence: 2, traceId: 'trace-1', atUnixMs: 1_001, monotonicMs: 2, stage: 'terminal' as const, event: 'keydown', outcome: 'composing' },
      ],
      daemons: [],
      conclusion: 'terminal_composing_no_transport',
    },
    {
      name: 'ready transport to daemon boundary',
      events: [
        { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'terminal' as const, event: 'keydown', outcome: 'sent' },
        { sequence: 2, traceId: 'trace-1', atUnixMs: 1_001, monotonicMs: 2, stage: 'transport' as const, event: 'websocket_send', socketState: 1, initialStateReceived: true },
      ],
      daemons: [],
      conclusion: 'transport_sent_no_daemon_evidence',
    },
    {
      name: 'unready transport queue',
      events: [
        { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'terminal' as const, event: 'keydown', outcome: 'sent' },
        { sequence: 2, traceId: 'trace-1', atUnixMs: 1_001, monotonicMs: 2, stage: 'transport' as const, event: 'websocket_send', socketState: 0, initialStateReceived: false },
      ],
      daemons: [],
      conclusion: 'transport_queued_while_unready',
    },
    {
      name: 'daemon to PTY boundary',
      events: [
        { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'terminal' as const, event: 'keydown', outcome: 'sent', runtimeId: 'runtime-1' },
        { sequence: 2, traceId: 'trace-1', atUnixMs: 1_001, monotonicMs: 2, stage: 'transport' as const, event: 'websocket_send', socketState: 1, initialStateReceived: true, runtimeId: 'runtime-1' },
      ],
      daemons: [daemon([{
        trace_id: 'trace-1', runtime_id: 'runtime-1', sequence: 1, received_at_unix_ms: 1_002,
        source: 'user', byte_count: 1, write_duration_us: 20, write_result: 'failed', error_class: 'broken_pipe',
      }])],
      conclusion: 'pty_write_failed',
    },
    {
      name: 'accepted input awaiting output',
      events: [
        { sequence: 1, traceId: 'trace-1', atUnixMs: 1_000, monotonicMs: 1, stage: 'terminal' as const, event: 'keydown', outcome: 'sent', runtimeId: 'runtime-1' },
        { sequence: 2, traceId: 'trace-1', atUnixMs: 1_001, monotonicMs: 2, stage: 'transport' as const, event: 'websocket_send', socketState: 1, initialStateReceived: true, runtimeId: 'runtime-1' },
      ],
      daemons: [daemon([{
        trace_id: 'trace-1', runtime_id: 'runtime-1', sequence: 1, received_at_unix_ms: 1_002,
        source: 'user', byte_count: 1, write_duration_us: 20, write_result: 'accepted',
      }])],
      conclusion: 'pty_write_succeeded_no_agent_evidence',
    },
  ])('identifies the $name', ({ events, daemons, conclusion }) => {
    expect(deriveInputJourneys(frontend(events), daemons, {})).toEqual([
      expect.objectContaining({ traceId: 'trace-1', conclusion }),
    ]);
  });

  it('requests the home daemon and each distinct session endpoint', async () => {
    const sendSupportSnapshot = vi.fn(async () => daemon([]));
    const capture = beginDiagnosticCapture({
      context: {
        capturedAtUnixMs: 1, view: 'sessions', activeSessionId: null, activePaneId: null,
        activeElement: 'terminal', documentFocused: true, visibility: 'visible',
        window: { width: 800, height: 600, devicePixelRatio: 2 },
      },
      panes: [
        { paneId: 'pane-local', runtimeId: 'runtime-local', sessionId: 'local', title: 'Local', sessionLabel: 'Local', workspaceId: 'w', workspaceLabel: 'Workspace', available: true },
        { paneId: 'pane-remote', runtimeId: 'runtime-remote', sessionId: 'remote-1a', title: 'Remote', sessionLabel: 'Remote', workspaceId: 'w', workspaceLabel: 'Workspace', available: true },
      ],
      workspaces: [], settings: {}, sendSupportSnapshot,
      sessions: [
        { id: 'local', label: 'Local', state: 'idle', agent: 'codex', cwd: '~', workspaceId: 'w', endpoint: 'local', active: false },
        { id: 'remote-1a', label: 'Remote', state: 'idle', agent: 'codex', cwd: '~', workspaceId: 'w', endpoint: 'remote', endpointId: 'endpoint-1', active: false },
        { id: 'remote-1b', label: 'Remote', state: 'idle', agent: 'codex', cwd: '~', workspaceId: 'w', endpoint: 'remote', endpointId: 'endpoint-1', active: false },
        { id: 'remote-2', label: 'Remote', state: 'idle', agent: 'codex', cwd: '~', workspaceId: 'w', endpoint: 'remote', endpointId: 'endpoint-2', active: false },
      ],
    });
    await capture.daemons;

    expect(sendSupportSnapshot.mock.calls).toEqual([
      [undefined, ['runtime-local']],
      ['endpoint-1', ['runtime-remote']],
      ['endpoint-2', []],
    ]);
  });
});

function pendingCapture(): PendingDiagnosticCapture {
  return {
    context: {
      capturedAtUnixMs: 1, view: 'sessions', activeSessionId: 'session-1', activePaneId: 'pane-1',
      activeElement: 'terminal', documentFocused: true, visibility: 'visible',
      window: { width: 800, height: 600, devicePixelRatio: 2 },
    },
    panes: [
      { paneId: 'pane-1', runtimeId: 'runtime-1', sessionId: 'session-1', title: 'First', sessionLabel: 'First', workspaceId: 'workspace-1', workspaceLabel: 'Workspace', available: true },
      { paneId: 'pane-2', runtimeId: 'runtime-2', sessionId: 'session-2', title: 'Second', sessionLabel: 'Second', workspaceId: 'workspace-1', workspaceLabel: 'Workspace', available: true },
    ],
    sessions: [], workspaces: [], settings: {}, frontendInput: frontend([]),
    terminalGeometry: {},
    terminalDiagnostics: { capacity: 3_000, total: 0, capturedAtUnixMs: 1, events: [] },
    uiDiagnostics: { capacity: 300, total: 0, capturedAtUnixMs: 1, events: [] },
    pty: {},
    daemons: Promise.resolve({ snapshots: [], unavailableEndpoints: [] }),
    nativeInput: Promise.resolve({ supported: false, os: 'linux', arch: 'x86_64', capacity: 512, total: 0, capturedAtUnixMs: 1, observations: [] }),
    historicalInput: Promise.resolve({ capacity: 256, total: 0, capturedAtUnixMs: 1, events: [] }),
  };
}

describe('diagnostic report pane consent', () => {
  it('reads no terminal content by default', async () => {
    const readPane = vi.fn(() => ({ text: 'PRIVATE_OUTPUT', available: true }));
    const report = await createDiagnosticReport(pendingCapture(), [], readPane);

    expect(readPane).not.toHaveBeenCalled();
    expect(report.paneContent).toEqual([]);
    expect(JSON.stringify(report)).not.toContain('PRIVATE_OUTPUT');
  });

  it('includes only selected output, strips controls, and marks unavailable panes', async () => {
    const report = await createDiagnosticReport(pendingCapture(), ['pane-1', 'pane-2'], (paneId) => (
      paneId === 'pane-1'
        ? { text: 'visible\u001b[31m\r\nPRIVATE_OUTPUT', available: true }
        : { text: '', available: false }
    ));

    expect(report.paneContent).toEqual([
      expect.objectContaining({ paneId: 'pane-1', text: 'visible[31m\nPRIVATE_OUTPUT', available: true }),
      { paneId: 'pane-2', runtimeId: 'runtime-2', available: false },
    ]);
    expect(report.omissions).toEqual(expect.arrayContaining([
      { section: 'paneContent:pane-2', reason: 'pane_not_mounted' },
    ]));
    expect(report.consent).toEqual(expect.objectContaining({
      paneContent: 'selected_panes', selectedPaneIds: ['pane-1', 'pane-2'],
    }));
  });

  it('bounds selected output by UTF-8 bytes and records the truncation receipt', async () => {
    const report = await createDiagnosticReport(pendingCapture(), ['pane-1'], () => ({
      text: `discarded${'😀'.repeat(20_000)}`,
      available: true,
    }));
    const pane = (report.paneContent as Array<Record<string, unknown>>)[0];

    expect(pane.bytes).toBeLessThanOrEqual(32 * 1024);
    expect(pane.truncatedAtStart).toBe(true);
    expect(pane.omittedBytes).toBeGreaterThan(0);
    expect(pane.text).not.toContain('discarded');
    expect(report.omissions).toEqual(expect.arrayContaining([
      expect.objectContaining({ section: 'paneContent:pane-1', reason: 'truncated_at_start' }),
    ]));
  });

  it('keeps the serialized report under its measured total-size limit', async () => {
    const capture = pendingCapture();
    capture.context.activePaneId = 'pane-0';
    capture.panes = Array.from({ length: 270 }, (_, index) => ({
      paneId: `pane-${index}`, runtimeId: `runtime-${index}`, sessionId: `session-${index}`,
      title: `Pane ${index}`, sessionLabel: `Session ${index}`, workspaceId: 'workspace-1',
      workspaceLabel: 'Workspace', available: true,
    }));
    const selected = capture.panes.map((pane) => pane.paneId);
    const report = await createDiagnosticReport(capture, selected, () => ({ text: 'x'.repeat(32 * 1024), available: true }));
    const bytes = new TextEncoder().encode(`${JSON.stringify(report, null, 2)}\n`).byteLength;

    expect(bytes).toBeLessThanOrEqual(8 * 1024 * 1024);
    expect((report.limits as { measuredBytes: number }).measuredBytes).toBe(bytes);
    expect(report.omissions).toEqual(expect.arrayContaining([
      expect.objectContaining({ reason: 'report_size_limit' }),
    ]));
  });
});
