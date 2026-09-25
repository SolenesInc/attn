import { act } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithDaemon } from '../test/renderApp';
import type { EventMessage } from '../test/protocol';

type CrewMember = EventMessage<'crew_updated'>['members'][number];

function member(id: string, bindingSession?: string, fields: Partial<CrewMember> = {}): CrewMember {
  return {
    id,
    charter_path: `/homes/${id}/CHARTER.md`,
    home_dir: `/homes/${id}`,
    awareness_dirs: [],
    resolved_agent: 'claude',
    revision: 1,
    ...(bindingSession ? { binding_session: bindingSession } : {}),
    ...fields,
  };
}

describe('useDaemonSocket crew', () => {
  async function renderWithCrew(crew: CrewMember[]) {
    return renderWithDaemon(null, { initialState: { crew } });
  }

  it('resolves a wake with the session to focus', async () => {
    const { daemon, api } = await renderWithCrew([member('keel')]);
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: true, member: 'keel', session_id: 'sess-keel',
    }));

    await expect(api.current.sendCrewWake('keel')).resolves.toEqual({ sessionId: 'sess-keel', alreadyAwake: false });
    expect(await daemon.received('crew_wake')).toMatchObject({ member: 'keel' });
  });

  it('resolves an already-awake member with its running day', async () => {
    const { daemon, api } = await renderWithCrew([member('keel', 'sess-keel')]);
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: true, member: 'keel', session_id: 'sess-keel', already_awake: true,
    }));

    await expect(api.current.sendCrewWake('keel')).resolves.toEqual({ sessionId: 'sess-keel', alreadyAwake: true });
  });

  it('rejects a refused wake with what the daemon said', async () => {
    const { daemon, api } = await renderWithCrew([member('keel')]);
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: false, error: 'keel launches in /gone, which is not there',
    }));

    await expect(api.current.sendCrewWake('keel')).rejects.toThrow('/gone');
  });

  it('asks an awake member to sleep and carries the delivery receipt', async () => {
    const { daemon, api } = await renderWithCrew([member('keel', 'sess-keel')]);
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result',
      success: true,
      member: 'keel',
      session_id: 'sess-keel',
      delivery_status: 'notified',
      detail: 'asked Keel to close its day and sleep',
    }));

    await expect(api.current.sendCrewSleep('keel')).resolves.toEqual({
      member: 'keel',
      sessionId: 'sess-keel',
      alreadyAsleep: false,
      deliveryStatus: 'notified',
      detail: 'asked Keel to close its day and sleep',
    });
    expect(await daemon.received('crew_sleep')).toMatchObject({ member: 'keel' });
  });

  it('names an already-asleep sleep request as a no-op', async () => {
    const { daemon, api } = await renderWithCrew([member('keel')]);
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: true, member: 'keel', already_asleep: true, detail: 'Keel is already asleep',
    }));

    await expect(api.current.sendCrewSleep('keel')).resolves.toEqual({
      member: 'keel',
      alreadyAsleep: true,
      detail: 'Keel is already asleep',
    });
  });

  it('rejects a refused sleep request with what the daemon said', async () => {
    const { daemon, api } = await renderWithCrew([member('keel', 'sess-keel')]);
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: false, error: 'session sess-keel cannot receive agent messages',
    }));

    await expect(api.current.sendCrewSleep('keel')).rejects.toThrow('cannot receive agent messages');
  });

  it('sends one atomic launch selection and returns a conflict with its authoritative member', async () => {
    const { daemon, api } = await renderWithCrew([member('keel')]);
    const current = member('keel', undefined, { revision: 9 });
    daemon.on('crew_set', () => ({
      event: 'crew_set_result', success: false, conflict: true, error: 'the member changed', member: current,
    }));

    await expect(api.current.sendCrewSet({
      member: 'keel', expectedRevision: 7, agent: 'codex', model: 'gpt-6-astra', effort: 'high',
    })).resolves.toEqual({ success: false, conflict: true, error: 'the member changed', member: current });
    expect(await daemon.received('crew_set')).toMatchObject({
      member: 'keel', expected_revision: 7, agent: 'codex', model: 'gpt-6-astra', effort: 'high',
    });
  });

  it('sends charter content with its CAS token and returns conflicts as data', async () => {
    const { daemon, api } = await renderWithCrew([member('alder')]);
    daemon.on('crew_charter_set', () => ({
      event: 'crew_charter_set_result', success: true, conflict: true,
      member: 'alder', charter: { content: '# External\n', token: 'external-token' },
    }));

    await expect(api.current.sendCrewCharterSet('alder', '# Mine\n', 'old-token')).resolves.toEqual({
      member: 'alder', conflict: true, charter: { content: '# External\n', token: 'external-token' },
    });
    expect(await daemon.received('crew_charter_set')).toMatchObject({
      member: 'alder', content: '# Mine\n', expected_token: 'old-token',
    });
  });

  it('reuses the caller restart identity and ignores another request result', async () => {
    const { daemon, api } = await renderWithCrew([member('keel', 'sess-keel')]);
    const restart = { request_id: 'restart-stable', session_id: 'sess-keel', state: 'queued' as const };
    const current = member('keel', 'sess-keel', { revision: 13, restart });
    daemon.on('crew_restart', () => [
      { event: 'crew_restart_result', request_id: 'another-request', success: true, conflict: false, member: current, restart },
      { event: 'crew_restart_result', success: true, conflict: false, member: current, restart },
    ]);

    await expect(api.current.sendCrewRestart({
      member: 'keel', requestId: 'restart-stable', expectedSessionId: 'sess-keel', expectedRevision: 12,
    })).resolves.toEqual({ success: true, conflict: false, member: current, restart });
    expect(await daemon.received('crew_restart')).toEqual({
      cmd: 'crew_restart', request_id: 'restart-stable', member: 'keel',
      expected_session_id: 'sess-keel', expected_revision: 12,
    });
  });

  it('leaves a constant-key request such as refresh_prs unsettled when sent again', async () => {
    const { daemon, api } = await renderWithCrew([member('keel', 'sess-keel')]);
    let firstSettled = false;
    void api.current.sendRefreshPRs().then(() => { firstSettled = true; }, () => { firstSettled = true; });
    const second = api.current.sendRefreshPRs();
    await daemon.idle();
    expect(firstSettled).toBe(false);

    daemon.emit({ event: 'refresh_prs_result', success: true });

    await expect(second).resolves.toBeDefined();
  });

  it('lets a redelivered restart supersede the earlier waiter under the same request id', async () => {
    const { daemon, api } = await renderWithCrew([member('keel', 'sess-keel')]);
    const options = { member: 'keel', requestId: 'restart-again', expectedSessionId: 'sess-keel', expectedRevision: 12 };
    const first = api.current.sendCrewRestart(options);
    const firstRejected = expect(first).rejects.toThrow('superseded');
    act(() => { vi.advanceTimersByTime(2_000); });
    const second = api.current.sendCrewRestart(options);
    await firstRejected;

    act(() => { vi.advanceTimersByTime(9_000); });
    const restart = { request_id: 'restart-again', session_id: 'sess-keel', state: 'queued' as const };
    const current = member('keel', 'sess-keel', { revision: 13, restart });
    daemon.emit({
      event: 'crew_restart_result', request_id: 'restart-again', success: true, conflict: false, member: current, restart,
    });

    await expect(second).resolves.toEqual({ success: true, conflict: false, member: current, restart });
  });
});
