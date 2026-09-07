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
  it('serializes and coalesces rapid changes without calling them saved early', async () => {
    const first = deferred<CrewMutationOutcome>();
    const second = deferred<CrewMutationOutcome>();
    const send = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const { result } = renderHook(() => useCrewLaunchAutosave([member('alder', 1)], 1, send));

    await waitFor(() => expect(result.current.read('alder')?.state).toBe('saved'));
    act(() => result.current.update('alder', { agent: 'codex', model: '', effort: '' }));
    act(() => result.current.update('alder', { agent: 'codex', model: 'gpt-6-astra', effort: '' }));
    act(() => result.current.update('alder', { agent: 'codex', model: 'gpt-6-astra', effort: 'high' }));

    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenNthCalledWith(1, {
      member: 'alder', expectedRevision: 1, agent: 'codex', model: '', effort: '',
    });
    expect(result.current.read('alder')).toMatchObject({
      state: 'saving', draft: { agent: 'codex', model: 'gpt-6-astra', effort: 'high' },
    });

    await act(async () => first.resolve({
      success: true,
      conflict: false,
      member: member('alder', 2, { agent: 'codex', resolved_agent: 'codex' }),
    }));
    expect(send).toHaveBeenCalledTimes(2);
    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'alder', expectedRevision: 2, agent: 'codex', model: 'gpt-6-astra', effort: 'high',
    });
    expect(result.current.read('alder')?.state).toBe('saving');

    await act(async () => second.resolve({
      success: true,
      conflict: false,
      member: member('alder', 3, {
        agent: 'codex', model: 'gpt-6-astra', effort: 'high',
        resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
      }),
    }));
    expect(result.current.read('alder')).toMatchObject({
      state: 'saved', draft: { agent: 'codex', model: 'gpt-6-astra', effort: 'high' },
      acknowledged: { revision: 3 },
    });
  });

  it('preserves an unrelated concurrent field and waits for review before retrying a conflict', async () => {
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
      });
    const initial = [member('keel', 7, {
      agent: 'codex', model: 'saved-model', effort: 'low',
      resolved_agent: 'codex', resolved_model: 'saved-model', resolved_effort: 'low',
    })];
    const { result } = renderHook(() => useCrewLaunchAutosave(initial, 1, send));
    await waitFor(() => expect(result.current.read('keel')).toBeDefined());

    act(() => result.current.update('keel', { model: 'local-model' }));

    await waitFor(() => expect(result.current.read('keel')?.state).toBe('error'));
    expect(send).toHaveBeenCalledTimes(1);
    expect(result.current.read('keel')?.draft).toEqual({ agent: 'codex', model: 'local-model', effort: 'high' });

    act(() => result.current.retry('keel'));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'keel', expectedRevision: 8, agent: 'codex', model: 'local-model', effort: 'high',
    });
    await waitFor(() => expect(result.current.read('keel')?.state).toBe('saved'));
  });

  it('keeps an explicit revert made while the replaced value is in flight', async () => {
    const first = deferred<CrewMutationOutcome>();
    const second = deferred<CrewMutationOutcome>();
    const send = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const initial = member('alder', 1, {
      agent: 'codex', model: 'model-a', resolved_agent: 'codex', resolved_model: 'model-a',
    });
    const { result, rerender } = renderHook(
      ({ members }) => useCrewLaunchAutosave(members, 1, send),
      { initialProps: { members: [initial] } },
    );
    await waitFor(() => expect(result.current.read('alder')).toBeDefined());

    act(() => result.current.update('alder', { model: 'model-b' }));
    act(() => result.current.update('alder', { model: 'model-a' }));

    rerender({ members: [{ ...initial, binding_session: 'derived-before-model-b-landed' }] });
    await waitFor(() => expect(result.current.read('alder')).toMatchObject({
      state: 'saving', draft: { model: 'model-a' },
      acknowledged: { revision: 1, binding_session: 'derived-before-model-b-landed' },
    }));

    const savedB = member('alder', 2, {
      agent: 'codex', model: 'model-b', resolved_agent: 'codex', resolved_model: 'model-b',
    });
    rerender({ members: [savedB] });
    await waitFor(() => expect(result.current.read('alder')).toMatchObject({
      state: 'saving', draft: { model: 'model-a' }, acknowledged: { revision: 2, model: 'model-b' },
    }));

    rerender({ members: [{ ...savedB, binding_session: 'derived-at-the-same-revision' }] });
    await waitFor(() => expect(result.current.read('alder')).toMatchObject({
      state: 'saving', draft: { model: 'model-a' },
      acknowledged: { revision: 2, binding_session: 'derived-at-the-same-revision' },
    }));

    await act(async () => first.resolve({
      success: true,
      conflict: false,
      member: savedB,
    }));

    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'alder', expectedRevision: 2, agent: 'codex', model: 'model-a', effort: '',
    });
    expect(result.current.read('alder')?.draft.model).toBe('model-a');

    await act(async () => second.resolve({
      success: true,
      conflict: false,
      member: member('alder', 3, {
        agent: 'codex', model: 'model-a', resolved_agent: 'codex', resolved_model: 'model-a',
      }),
    }));
    expect(result.current.read('alder')?.state).toBe('saved');
  });

  it('preserves explicit dependency clears when a concurrent harness conflict fills them', async () => {
    const send = vi.fn()
      .mockResolvedValueOnce({
        success: false,
        conflict: true,
        member: member('keel', 2, {
          agent: 'claude', model: 'external-model', effort: 'high',
          resolved_agent: 'claude', resolved_model: 'external-model', resolved_effort: 'high',
        }),
      })
      .mockResolvedValueOnce({
        success: true,
        conflict: false,
        member: member('keel', 3, { agent: 'codex', resolved_agent: 'codex' }),
      });
    const { result } = renderHook(() => useCrewLaunchAutosave([
      member('keel', 1, { agent: 'claude', resolved_agent: 'claude' }),
    ], 1, send));
    await waitFor(() => expect(result.current.read('keel')).toBeDefined());

    act(() => result.current.update('keel', { agent: 'codex', model: '', effort: '' }));
    await waitFor(() => expect(result.current.read('keel')?.state).toBe('error'));
    expect(result.current.read('keel')?.draft).toEqual({ agent: 'codex', model: '', effort: '' });

    act(() => result.current.retry('keel'));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'keel', expectedRevision: 2, agent: 'codex', model: '', effort: '',
    });
    await waitFor(() => expect(result.current.read('keel')?.state).toBe('saved'));
  });

  it('accepts changed daemon-derived state at the same document revision', async () => {
    const initial = member('trellis', 4, { resolved_agent: 'claude' });
    const { result, rerender } = renderHook(
      ({ members }) => useCrewLaunchAutosave(members, 1, vi.fn()),
      { initialProps: { members: [initial] } },
    );
    await waitFor(() => expect(result.current.read('trellis')).toBeDefined());

    rerender({ members: [member('trellis', 4, {
      binding_session: 'session-new',
      resolved_agent: 'codex',
      resolved_model: 'gpt-6-astra',
    })] });

    await waitFor(() => expect(result.current.read('trellis')?.acknowledged).toMatchObject({
      revision: 4,
      binding_session: 'session-new',
      resolved_agent: 'codex',
      resolved_model: 'gpt-6-astra',
    }));
  });

  it('keeps a failed edit and retries it after reconnect', async () => {
    const send = vi.fn()
      .mockRejectedValueOnce(new Error('WebSocket not connected'))
      .mockResolvedValueOnce({
        success: true, conflict: false,
        member: member('trellis', 2, { model: 'private-model', resolved_model: 'private-model' }),
      });
    const props = { connectionGeneration: 1, members: [member('trellis', 1)] };
    const { result, rerender } = renderHook(
      ({ connectionGeneration, members }) => useCrewLaunchAutosave(members, connectionGeneration, send),
      { initialProps: props },
    );
    await waitFor(() => expect(result.current.read('trellis')).toBeDefined());
    act(() => result.current.update('trellis', { agent: '', model: 'private-model', effort: '' }));
    await waitFor(() => expect(result.current.read('trellis')?.state).toBe('error'));
    expect(result.current.read('trellis')?.draft.model).toBe('private-model');

    rerender({ connectionGeneration: 2, members: [member('trellis', 1)] });

    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(result.current.read('trellis')).toMatchObject({ state: 'saved', acknowledged: { revision: 2 } }));
  });

  it('retries a transport-uncertain write even after a matching broadcast', async () => {
    const retry = deferred<CrewMutationOutcome>();
    const send = vi.fn()
      .mockRejectedValueOnce(new Error('WebSocket not connected'))
      .mockReturnValueOnce(retry.promise);
    const { result, rerender } = renderHook(
      ({ members, connectionGeneration }) => useCrewLaunchAutosave(members, connectionGeneration, send),
      { initialProps: { members: [member('trellis', 1)], connectionGeneration: 0 } },
    );
    await waitFor(() => expect(result.current.read('trellis')).toBeDefined());
    act(() => result.current.update('trellis', { model: 'private-model' }));
    await waitFor(() => expect(result.current.read('trellis')?.state).toBe('error'));

    const landed = member('trellis', 2, { model: 'private-model', resolved_model: 'private-model' });
    rerender({ members: [landed], connectionGeneration: 0 });

    await waitFor(() => expect(result.current.read('trellis')).toMatchObject({
      state: 'error', draft: { model: 'private-model' }, acknowledged: { revision: 2 },
    }));

    rerender({ members: [landed], connectionGeneration: 1 });
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send).toHaveBeenLastCalledWith({
      member: 'trellis', expectedRevision: 2, agent: '', model: 'private-model', effort: '',
    });
    await act(async () => retry.resolve({
      success: true,
      conflict: false,
      member: member('trellis', 3, { model: 'private-model', resolved_model: 'private-model' }),
    }));
    expect(result.current.read('trellis')?.state).toBe('saved');
  });

  it('does not let a rejected retry make an older uncertain write look settled', async () => {
    const finalRetry = deferred<CrewMutationOutcome>();
    const send = vi.fn()
      .mockRejectedValueOnce(new Error('WebSocket not connected'))
      .mockResolvedValueOnce({ success: false, conflict: false, error: 'invalid model' })
      .mockReturnValueOnce(finalRetry.promise);
    const initial = member('trellis', 1, {
      agent: 'codex', model: 'model-a', resolved_agent: 'codex', resolved_model: 'model-a',
    });
    const { result, rerender } = renderHook(
      ({ members }) => useCrewLaunchAutosave(members, 0, send),
      { initialProps: { members: [initial] } },
    );
    await waitFor(() => expect(result.current.read('trellis')).toBeDefined());

    act(() => result.current.update('trellis', { model: 'model-b' }));
    await waitFor(() => expect(result.current.read('trellis')?.state).toBe('error'));
    act(() => result.current.update('trellis', { model: 'model-a' }));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(result.current.read('trellis')).toMatchObject({
      state: 'error', draft: { model: 'model-a' }, error: 'invalid model',
    }));

    rerender({ members: [{ ...initial, binding_session: 'derived-at-revision-one' }] });
    await waitFor(() => expect(result.current.read('trellis')).toMatchObject({
      state: 'error', draft: { model: 'model-a' },
      acknowledged: { revision: 1, binding_session: 'derived-at-revision-one' },
    }));

    act(() => result.current.retry('trellis'));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(3));
    expect(send).toHaveBeenLastCalledWith({
      member: 'trellis', expectedRevision: 1, agent: 'codex', model: 'model-a', effort: '',
    });
    await act(async () => finalRetry.resolve({
      success: true,
      conflict: false,
      member: member('trellis', 2, {
        agent: 'codex', model: 'model-a', resolved_agent: 'codex', resolved_model: 'model-a',
      }),
    }));
    expect(result.current.read('trellis')?.state).toBe('saved');
  });

  it('does not let a late result erase a newer local edit or roster revision', async () => {
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
    act(() => result.current.update('alder', { agent: '', model: 'first', effort: '' }));
    act(() => result.current.update('alder', { agent: '', model: 'latest', effort: 'high' }));
    rerender({ members: [member('alder', 6, { model: 'external', resolved_model: 'external' })] });

    await act(async () => first.resolve({
      success: true, conflict: false,
      member: member('alder', 4, { model: 'first', resolved_model: 'first' }),
    }));

    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send).toHaveBeenNthCalledWith(2, {
      member: 'alder', expectedRevision: 6, agent: '', model: 'latest', effort: 'high',
    });
    expect(result.current.read('alder')?.draft).toEqual({ agent: '', model: 'latest', effort: 'high' });
  });
});
