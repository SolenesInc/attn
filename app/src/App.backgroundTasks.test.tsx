import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import { openSection } from './test/settings';

const AT = '2026-05-16T16:00:00Z';

describe('App background tasks', () => {
  it('shows a dead task’s cause and keeps its diagnostic output behind an expander', async () => {
    const daemon = await openSection('backgroundTasks', {}, (scripted) => {
      scripted.on('task_list', () => ({
        event: 'task_list_result',
        success: true,
        tasks: [{
          id: 'task-1',
          kind: 'session_activity',
          subject: 'session-1',
          state: 'dead',
          attempts: 5,
          next_attempt_at: AT,
          last_error: 'The agent process exited with status 2.',
          last_diagnostic: 'stderr: authentication failed',
          created_at: AT,
          updated_at: AT,
        }],
      }));
    });

    expect(screen.getByText('The agent process exited with status 2.')).toBeInTheDocument();
    expect(screen.getByText('stderr: authentication failed')).not.toBeVisible();

    await gesture(daemon, () => fireEvent.click(screen.getByText('Diagnostic output')));

    expect(screen.getByText('stderr: authentication failed')).toBeVisible();
    expect(daemon.sentOf('task_list')).toHaveLength(1);
  });
});
