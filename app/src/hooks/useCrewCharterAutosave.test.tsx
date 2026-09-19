import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useCrewCharterAutosave } from './useCrewCharterAutosave';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

const charter = (content: string, token: string) => ({ member: 'trellis', charter: { content, token } });

afterEach(() => {
  vi.useRealTimers();
});

describe('useCrewCharterAutosave', () => {
  it('writes after the typing pause with the acknowledged token and flushes on demand', async () => {
    vi.useFakeTimers();
    const save = deferred<any>();
    const getCharter = vi.fn().mockResolvedValue(charter('# One\n', 'one'));
    const setCharter = vi.fn().mockReturnValue(save.promise);
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));

    await act(async () => { await result.current.load('trellis'); });
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: '# One\n' });
    act(() => result.current.update('trellis', '# Two\n'));
    expect(result.current.read('trellis')?.state).toBe('dirty');
    await act(async () => { await vi.advanceTimersByTimeAsync(699); });
    expect(setCharter).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(result.current.read('trellis')?.state).toBe('saving');
    expect(setCharter).toHaveBeenCalledExactlyOnceWith('trellis', '# Two\n', 'one');

    await act(async () => save.resolve({ member: 'trellis', conflict: false, charter: { content: '# Two\n', token: 'two' } }));
    expect(result.current.read('trellis')?.state).toBe('saved');

    act(() => result.current.update('trellis', '# Three\n'));
    await act(async () => { await result.current.flush('trellis'); });
    expect(setCharter).toHaveBeenNthCalledWith(2, 'trellis', '# Three\n', 'two');
    await act(async () => { await vi.advanceTimersByTimeAsync(700); });
    expect(setCharter).toHaveBeenCalledTimes(2);
  });

  it('reports a failed first load with a retry and keeps a stale reread from replacing a newer write', async () => {
    const staleRead = deferred<any>();
    const getCharter = vi.fn()
      .mockRejectedValueOnce(new Error('charter unreadable'))
      .mockResolvedValueOnce(charter('A', 'a'))
      .mockReturnValueOnce(staleRead.promise);
    const setCharter = vi.fn().mockResolvedValue({ member: 'trellis', conflict: false, charter: { content: 'B', token: 'b' } });
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));

    await act(async () => { await result.current.load('trellis'); });
    expect(result.current.read('trellis')).toMatchObject({ state: 'error', error: 'charter unreadable' });
    await act(async () => { await result.current.load('trellis'); });
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: 'A' });

    let reread!: Promise<void>;
    act(() => { reread = result.current.load('trellis'); });
    act(() => result.current.update('trellis', 'B'));
    await act(async () => { await result.current.flush('trellis'); });
    await act(async () => staleRead.resolve(charter('A', 'a')));
    await reread;
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: 'B', acknowledged: { token: 'b' } });
  });

  it('turns a reread with a different token into a conflict while an edit is unsaved', async () => {
    const getCharter = vi.fn()
      .mockResolvedValueOnce(charter('old', 'old-token'))
      .mockResolvedValueOnce(charter('external', 'external-token'));
    const setCharter = vi.fn().mockResolvedValue({ member: 'trellis', conflict: false, charter: { content: 'mine', token: 'mine-token' } });
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));
    await act(async () => { await result.current.load('trellis'); });

    act(() => result.current.update('trellis', 'mine'));
    await act(async () => { await result.current.load('trellis'); });
    expect(result.current.read('trellis')).toMatchObject({ state: 'conflict', draft: 'mine', conflict: { content: 'external' } });

    await act(async () => { await result.current.keepMine('trellis'); });
    expect(setCharter).toHaveBeenCalledExactlyOnceWith('trellis', 'mine', 'external-token');
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: 'mine' });
  });

  it('rereads every loaded charter after reconnect', async () => {
    const getCharter = vi.fn()
      .mockResolvedValueOnce(charter('disk', 'disk-token'))
      .mockResolvedValueOnce(charter('changed on disk', 'disk-token-2'));
    const { result, rerender } = renderHook(
      ({ generation }) => useCrewCharterAutosave(generation, getCharter, vi.fn()),
      { initialProps: { generation: 1 } },
    );
    await act(async () => { await result.current.load('trellis'); });

    await act(async () => rerender({ generation: 2 }));
    expect(getCharter).toHaveBeenCalledTimes(2);
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: 'changed on disk' });
  });
});
