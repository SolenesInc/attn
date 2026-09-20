import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { CrewMember } from '../types/generated';
import type { CrewMutationOutcome } from './daemonCrewEvents';
import { useCrewLaunchAutosave } from './useCrewLaunchAutosave';

function member(id: string, revision: number, selection: Partial<CrewMember> = {}): CrewMember {
  return {
    id,
    revision,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    resolved_agent: selection.agent || 'claude',
    ...selection,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe('useCrewLaunchAutosave', () => {
  it('settles a pending edit from a matching roster broadcast unless the write outcome is unknown', async () => {
    const send = vi.fn()
      .mockReturnValueOnce(new Promise<never>(() => {}))
      .mockRejectedValueOnce(new Error('WebSocket not connected'));
    const { result, rerender } = renderHook(
      ({ members }) => useCrewLaunchAutosave(members, 0, send),
      { initialProps: { members: [member('trellis', 1)] } },
    );
    await waitFor(() => expect(result.current.read('trellis')).toBeDefined());

    act(() => result.current.update('trellis', { model: 'private-model' }));
    const landed = member('trellis', 2, { model: 'private-model', resolved_model: 'private-model' });
    rerender({ members: [landed] });
    expect(result.current.read('trellis')).toMatchObject({ state: 'saving', acknowledged: { revision: 2 } });

    rerender({ members: [member('trellis', 1)] });
    expect(result.current.read('trellis')?.acknowledged.revision).toBe(2);

    const { result: lost, rerender: rerenderLost } = renderHook(
      ({ members }) => useCrewLaunchAutosave(members, 0, send),
      { initialProps: { members: [member('keel', 1)] } },
    );
    await waitFor(() => expect(lost.current.read('keel')).toBeDefined());
    act(() => lost.current.update('keel', { model: 'private-model' }));
    await waitFor(() => expect(lost.current.read('keel')?.state).toBe('error'));
    rerenderLost({ members: [member('keel', 2, { model: 'private-model', resolved_model: 'private-model' })] });
    expect(lost.current.read('keel')).toMatchObject({
      state: 'error', draft: { model: 'private-model' }, acknowledged: { revision: 2 },
    });
  });

  it('adopts changed daemon-derived state at the same revision and drops a write result older than the roster', async () => {
    const first = deferred<CrewMutationOutcome>();
    const send = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({
        success: true, conflict: false,
        member: member('alder', 7, { model: 'latest', effort: 'high', resolved_model: 'latest', resolved_effort: 'high' }),
      });
    const { result, rerender } = renderHook(
      ({ members }) => useCrewLaunchAutosave(members, 1, send),
      { initialProps: { members: [member('alder', 3)] } },
    );
    await waitFor(() => expect(result.current.read('alder')).toBeDefined());

    rerender({ members: [member('alder', 3, { binding_session: 'session-new', resolved_agent: 'codex' })] });
    expect(result.current.read('alder')?.acknowledged).toMatchObject({ revision: 3, binding_session: 'session-new', resolved_agent: 'codex' });

    act(() => result.current.update('alder', { model: 'first' }));
    act(() => result.current.update('alder', { model: 'latest', effort: 'high' }));
    rerender({ members: [member('alder', 6, { model: 'external', resolved_model: 'external' })] });
    await act(async () => first.resolve({
      success: true, conflict: false,
      member: member('alder', 4, { model: 'first', resolved_model: 'first' }),
    }));

    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'alder', expectedRevision: 6, agent: '', model: 'latest', effort: 'high',
    });
    expect(result.current.read('alder')).toMatchObject({ state: 'saved', acknowledged: { revision: 7 } });
  });

  it('merges untouched fields from a conflicting member while keeping explicit clears', async () => {
    const send = vi.fn()
      .mockResolvedValueOnce({
        success: false, conflict: true,
        member: member('keel', 8, {
          agent: 'codex', model: 'saved-model', effort: 'high',
          resolved_agent: 'codex', resolved_model: 'saved-model', resolved_effort: 'high',
        }),
      })
      .mockResolvedValueOnce({
        success: true, conflict: false,
        member: member('keel', 9, {
          agent: 'codex', model: 'local-model', effort: 'high',
          resolved_agent: 'codex', resolved_model: 'local-model', resolved_effort: 'high',
        }),
      })
      .mockResolvedValueOnce({
        success: false, conflict: true,
        member: member('keel', 10, {
          agent: 'codex', model: 'local-model', effort: 'high',
          resolved_agent: 'codex', resolved_model: 'local-model', resolved_effort: 'high',
        }),
      })
      .mockResolvedValueOnce({
        success: true, conflict: false,
        member: member('keel', 11, { agent: 'claude', resolved_agent: 'claude' }),
      });
    const { result } = renderHook(() => useCrewLaunchAutosave([member('keel', 7, {
      agent: 'codex', model: 'saved-model', effort: 'low',
      resolved_agent: 'codex', resolved_model: 'saved-model', resolved_effort: 'low',
    })], 1, send));
    await waitFor(() => expect(result.current.read('keel')).toBeDefined());

    act(() => result.current.update('keel', { model: 'local-model' }));
    await waitFor(() => expect(result.current.read('keel')?.state).toBe('error'));
    expect(result.current.read('keel')).toMatchObject({
      draft: { agent: 'codex', model: 'local-model', effort: 'high' },
      error: 'Launch settings changed elsewhere. Review the saved values and retry.',
    });
    await act(async () => { await result.current.retry('keel'); });
    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'keel', expectedRevision: 8, agent: 'codex', model: 'local-model', effort: 'high',
    });
    expect(result.current.read('keel')?.state).toBe('saved');

    act(() => result.current.update('keel', { agent: 'claude', model: '', effort: '' }));
    await waitFor(() => expect(result.current.read('keel')?.state).toBe('error'));
    expect(result.current.read('keel')?.draft).toEqual({ agent: 'claude', model: '', effort: '' });
    await act(async () => { await result.current.retry('keel'); });
    expect(send).toHaveBeenNthCalledWith(4, {
      member: 'keel', expectedRevision: 10, agent: 'claude', model: '', effort: '',
    });
    expect(result.current.read('keel')?.state).toBe('saved');
  });
});
