import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../store/sessions';
import { agentDesktop, arrangeDesktops, TEST_PROFILE_ID } from '../test/desktops';
import { useAppDiagnostics } from './useAppDiagnostics';

const { beginDiagnosticCapture } = vi.hoisted(() => ({
  beginDiagnosticCapture: vi.fn((_input: unknown) => ({ id: 'capture' })),
}));
vi.mock('../utils/diagnosticReport', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../utils/diagnosticReport')>()),
  beginDiagnosticCapture,
}));

function session(id: string, desktopId: string): Session {
  return {
    id,
    label: id,
    state: 'idle',
    cwd: `/repo/${id}`,
    workspaceId: `legacy-${id}`,
    profileId: TEST_PROFILE_ID,
    desktopId,
    agent: 'claude',
    transcriptMatched: true,
    daemonActivePaneId: '',
    desktop: { agents: [], layoutTree: null },
  };
}

describe('useAppDiagnostics', () => {
  beforeEach(() => beginDiagnosticCapture.mockClear());

  it('attributes each pane to its own agent and reports each desktop once', async () => {
    arrangeDesktops([agentDesktop('d1', 1, ['s1', 's2'], 's2'), agentDesktop('d2', 2, ['s3'])]);
    const { result } = renderHook(() =>
      useAppDiagnostics({
        sessions: [session('s1', 'd1'), session('s2', 'd1'), session('s3', 'd2')],
        getPaneSize: () => ({ cols: 80, rows: 24 }),
        activeSessionId: 's2',
        getActivePaneIdForSession: () => 'pane-s2',
        view: 'session',
        settings: {},
        sendSupportSnapshot: vi.fn(),
        getPaneText: () => '',
      }),
    );

    await act(async () => {
      await result.current.handleCreateDiagnosticReport();
    });

    const input = beginDiagnosticCapture.mock.calls[0][0] as {
      panes: Array<{ paneId: string; sessionId: string; workspaceId: string }>;
      workspaces: Array<{ id: string; directory: string }>;
    };
    expect(input.panes.map(({ paneId, sessionId, workspaceId }) => [paneId, sessionId, workspaceId])).toEqual([
      ['pane-s1', 's1', 'd1'],
      ['pane-s2', 's2', 'd1'],
      ['pane-s3', 's3', 'd2'],
    ]);
    expect(input.workspaces.map((entry) => entry.id)).toEqual(['d1', 'd2']);
    expect(input.workspaces[0].directory).toContain('s2');
  });
});
