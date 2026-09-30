import { act } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { laidOutWorkspace, pane, renderWorkspace, split } from './test/workspaces';

it('keeps reattaching when a completed request deadline passes during the next attach', async () => {
  const root = split('source', 'vertical', [pane('s1'), pane('s2')]);
  const { daemon } = await renderWorkspace(root, ['s1', 's2']);
  await openSession(daemon, 's1');
  const attaches = () => daemon.sentOf('attach_session').filter((command) => command.id === 's1');
  expect(attaches()).toHaveLength(1);

  const remount = async () => {
    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: laidOutWorkspace(pane('s2'), ['s2']).layout! });
    await daemon.idle();
    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: laidOutWorkspace(root, ['s1', 's2']).layout! });
    await daemon.idle();
  };

  // Start a new attach just before the completed request's 15-second deadline.
  await act(() => vi.advanceTimersByTimeAsync(14_990));
  daemon.on('attach_session', () => undefined);
  await remount();
  expect(attaches()).toHaveLength(2);
  await act(() => vi.advanceTimersByTimeAsync(20));
  daemon.emit({ event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, running: true, last_seq: 0 });
  await daemon.idle();

  await remount();
  expect(attaches()).toHaveLength(3);
  daemon.emit({ event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, running: true, last_seq: 0 });
  daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('after-the-remount\x1b[5n') });
  await daemon.idle();
  expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).toContain('after-the-remount');
  expect(daemon.sentOf('pty_input')).toContainEqual({ cmd: 'pty_input', id: 's1', data: '\x1b[0n', source: 'response' });
});
