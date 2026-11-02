import { describe, expect, it } from 'vitest';
import { GatewaySessionState } from '../types/gatewayGenerated';
import { decodeGatewayEvent } from './decode';

const session = { id: 's1', title: 'Fix the build', agent: 'claude', offline: false };

describe('decodeGatewayEvent', () => {
  it('decodes a session state this build does not know as unknown', () => {
    const decoded = decodeGatewayEvent(JSON.stringify({
      event: 'sessions',
      sessions: [
        { ...session, state: 'dreaming', mood: 'calm' },
        { ...session, id: 's2', state: 'waiting', waiting_since: '2026-11-02T10:00:00Z' },
      ],
    }));

    expect(decoded).toMatchObject({
      event: 'sessions',
      sessions: [
        { id: 's1', state: GatewaySessionState.Unknown },
        { id: 's2', state: GatewaySessionState.Waiting, waiting_since: new Date('2026-11-02T10:00:00Z') },
      ],
    });
  });

  it('decodes the pong a home sends in answer to ping', () => {
    expect(decodeGatewayEvent(JSON.stringify({ event: 'pong' }))).toEqual({ event: 'pong' });
  });

  it('skips an event this build does not know', () => {
    expect(decodeGatewayEvent(JSON.stringify({ event: 'terminal_screen', terminal_id: 't1' }))).toBeNull();
  });

  it('refuses a known event that breaks its shape', () => {
    expect(() => decodeGatewayEvent(JSON.stringify({ event: 'sessions', sessions: [{ id: 's1', state: 'idle' }] }))).toThrow();
  });
});
