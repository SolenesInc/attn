import { describe, expect, it } from 'vitest';
import { HomeGatewayAskQuestionKind, HomeGatewayCheckState, HomeGatewaySeedStatus, HomeGatewaySessionState } from '../types/homeGatewayGenerated';
import { decodeHomeGatewayEvent } from './decode';

const session = { id: 's1', title: 'Fix the build', agent: 'claude', offline: false };

describe('decodeHomeGatewayEvent', () => {
  it('decodes a session state this build does not know as unknown', () => {
    const decoded = decodeHomeGatewayEvent(JSON.stringify({
      event: 'sessions',
      sessions: [
        { ...session, state: 'dreaming', mood: 'calm' },
        { ...session, id: 's2', state: 'waiting', waiting_since: '2026-11-02T10:00:00Z' },
      ],
    }));

    expect(decoded).toMatchObject({
      event: 'sessions',
      sessions: [
        { id: 's1', state: HomeGatewaySessionState.Unknown },
        { id: 's2', state: HomeGatewaySessionState.Waiting, waiting_since: new Date('2026-11-02T10:00:00Z') },
      ],
    });
  });

  it('decodes a question kind this build does not know as unknown', () => {
    const decoded = decodeHomeGatewayEvent(JSON.stringify({
      event: 'ask',
      ask: {
        id: 'a-1',
        session_id: 's1',
        subject: 'Rank these',
        created_at: '2026-11-02T10:00:00Z',
        parts: [
          { markdown: 'Context.' },
          { question: { key: 'q1', kind: 'rank', prompt: 'Rank these', choices: [{ label: 'A' }] } },
          { question: { key: 'q2', kind: 'one', prompt: 'Pick one', choices: [{ label: 'B' }] } },
        ],
      },
    }));

    expect(decoded).toMatchObject({
      event: 'ask',
      ask: {
        id: 'a-1',
        created_at: new Date('2026-11-02T10:00:00Z'),
        parts: [
          { markdown: 'Context.' },
          { question: { key: 'q1', kind: HomeGatewayAskQuestionKind.Unknown } },
          { question: { key: 'q2', kind: HomeGatewayAskQuestionKind.One } },
        ],
      },
    });
  });

  it('decodes a seed status and a check state this build does not know as unknown', () => {
    const seed = {
      id: 's-1', slug: 'read-seeds', title: 'Read seeds', claimed: false, ready: true, waits_for: [],
      state_changed_at: '2026-11-02T10:00:00Z',
    };
    expect(decodeHomeGatewayEvent(JSON.stringify({
      event: 'garden_seeds',
      seeds: [{ ...seed, status: 'dormant' }, { ...seed, id: 's-2', status: 'growing' }],
    }))).toMatchObject({
      seeds: [{ id: 's-1', status: HomeGatewaySeedStatus.Unknown }, { id: 's-2', status: HomeGatewaySeedStatus.Growing }],
    });
    expect(decodeHomeGatewayEvent(JSON.stringify({
      event: 'seed_detail', request_id: 'r1', body: '', log: [], log_total: 0,
      seed: { ...seed, status: 'dormant' },
      children: [{ ...seed, status: 'harvested' }],
      checks: [{ id: 'C1', text: 'Proven', state: 'waived' }],
    }))).toMatchObject({
      seed: { status: HomeGatewaySeedStatus.Unknown },
      children: [{ status: HomeGatewaySeedStatus.Harvested }],
      checks: [{ id: 'C1', state: HomeGatewayCheckState.Unknown }],
    });
  });

  it('decodes the pong a home sends in answer to ping', () => {
    expect(decodeHomeGatewayEvent(JSON.stringify({ event: 'pong' }))).toEqual({ event: 'pong' });
  });

  it('skips an event this build does not know', () => {
    expect(decodeHomeGatewayEvent(JSON.stringify({ event: 'terminal_mood', terminal_id: 't1' }))).toBeNull();
  });

  it('refuses a known event that breaks its shape', () => {
    expect(() => decodeHomeGatewayEvent(JSON.stringify({ event: 'sessions', sessions: [{ id: 's1', state: 'idle' }] }))).toThrow();
  });
});
