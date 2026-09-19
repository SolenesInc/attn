import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { BackgroundTasksSettings } from './BackgroundTasksSettings';
import type { Task } from '../hooks/useDaemonSocket';

describe('BackgroundTasksSettings', () => {
  it('keeps the safe cause separate from expandable diagnostic output', async () => {
    const task: Task = {
      id: 'task-1',
      kind: 'session_activity',
      subject: 'session-1',
      state: 'dead',
      attempts: 5,
      next_attempt_at: new Date().toISOString(),
      last_error: 'The agent process exited with status 2.',
      last_diagnostic: 'stderr: authentication failed',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    render(
      <BackgroundTasksSettings
        listTasks={vi.fn().mockResolvedValue([task])}
        retryTask={vi.fn().mockResolvedValue(null)}
        taskChangeSignal={0}
      />,
    );

    await waitFor(() => expect(screen.getByText('The agent process exited with status 2.')).toBeInTheDocument());
    await userEvent.click(screen.getByText('Diagnostic output'));
    expect(screen.getByText('stderr: authentication failed')).toBeInTheDocument();
  });
});
