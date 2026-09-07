import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DiagnosticReportPrompt } from './DiagnosticReportPrompt';
import type { PendingDiagnosticCapture } from '../utils/diagnosticReport';

function capture(): PendingDiagnosticCapture {
  return {
    context: {
      capturedAtUnixMs: 1, view: 'sessions', activeSessionId: 'session-1', activePaneId: 'pane-1',
      activeElement: 'terminal', documentFocused: true, visibility: 'visible',
      window: { width: 800, height: 600, devicePixelRatio: 2 },
    },
    panes: [
      { paneId: 'pane-1', runtimeId: 'runtime-1', sessionId: 'session-1', title: 'Affected pane', sessionLabel: 'First session', workspaceId: 'workspace-1', workspaceLabel: 'Workspace', available: true },
      { paneId: 'pane-2', runtimeId: 'runtime-2', sessionId: 'session-2', title: 'Other pane', sessionLabel: 'Second session', workspaceId: 'workspace-1', workspaceLabel: 'Workspace', available: true },
    ],
    sessions: [], workspaces: [], settings: {},
    frontendInput: { capacity: 512, total: 0, capturedAtUnixMs: 1, capturedAtMonotonicMs: 1, events: [] },
    terminalGeometry: {},
    terminalDiagnostics: { capacity: 3_000, total: 0, capturedAtUnixMs: 1, events: [] },
    uiDiagnostics: { capacity: 300, total: 0, capturedAtUnixMs: 1, events: [] },
    pty: {},
    daemons: Promise.resolve({ snapshots: [], unavailableEndpoints: [] }),
    nativeInput: Promise.resolve({ supported: false, os: 'linux', arch: 'x86_64', capacity: 512, total: 0, capturedAtUnixMs: 1, observations: [] }),
    historicalInput: Promise.resolve({ capacity: 256, total: 0, capturedAtUnixMs: 1, events: [] }),
  };
}

describe('DiagnosticReportPrompt', () => {
  it('preselects only the affected pane and sends the explicit selection', async () => {
    const onCreate = vi.fn(async () => {});
    render(<DiagnosticReportPrompt capture={capture()} affectedPaneId="pane-1" onCreate={onCreate} onClose={vi.fn()} />);

    expect(screen.getByRole('checkbox', { name: /Affected pane/ })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: /Other pane/ })).not.toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: /Other pane/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save report' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(['pane-1', 'pane-2']));
  });

  it('can create a metadata-only report after clearing output consent', async () => {
    const onCreate = vi.fn(async () => {});
    render(<DiagnosticReportPrompt capture={capture()} affectedPaneId="pane-1" onCreate={onCreate} onClose={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save report' }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledWith([]));
  });
});
