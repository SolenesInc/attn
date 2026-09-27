import { describe, expect, it } from 'vitest';
import { paneNotice } from './paneNotice';

type AgentPane = Parameters<typeof paneNotice>[0];
type PaneSession = NonNullable<Parameters<typeof paneNotice>[1]>;

const pane = (overrides: Partial<AgentPane>): AgentPane =>
  ({ id: 'pane-1', sessionId: 'session-1', runtimeId: 'runtime-1', ...overrides }) as AgentPane;
const session = { id: 'session-1' } as PaneSession;

describe('paneNotice', () => {
  it('names the failure for a failed pane', () => {
    expect(paneNotice(pane({ status: 'failed', error: 'spawn refused' }), session, 'claude')).toEqual({
      tone: 'failed',
      text: 'spawn refused',
    });
    expect(paneNotice(pane({ status: 'failed' }), undefined, 'claude')).toEqual({
      tone: 'failed',
      text: 'Session failed to start',
    });
  });

  it('shows a spinner while the pane spawns', () => {
    expect(paneNotice(pane({ status: 'spawning' }), undefined, 'claude')).toEqual({
      tone: 'spawning',
      text: 'Starting claude...',
    });
  });

  it('waits for a ready pane whose session has not arrived', () => {
    expect(paneNotice(pane({ status: 'ready' }), undefined, 'claude')).toEqual({
      tone: 'spawning',
      text: 'Waiting for claude...',
    });
    expect(paneNotice(pane({}), undefined, 'claude')?.text).toBe('Waiting for claude...');
  });

  it('renders the terminal once the session is live', () => {
    expect(paneNotice(pane({ status: 'ready' }), session, 'claude')).toBeNull();
  });
});
