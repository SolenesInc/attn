import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useCrewCharterAutosave } from './useCrewCharterAutosave';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

afterEach(() => {
  vi.useRealTimers();
});

describe('useCrewCharterAutosave', () => {
  it('waits for the established typing pause and never reports Saved before acknowledgement', async () => {
    vi.useFakeTimers();
    const save = deferred<any>();
    const getCharter = vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: '# One\n', token: 'one' } });
    const setCharter = vi.fn().mockReturnValue(save.promise);
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));

    await act(async () => { await result.current.load('trellis'); });
    expect(result.current.read('trellis')?.state).toBe('saved');
    act(() => result.current.update('trellis', '# Two\n'));
    expect(result.current.read('trellis')?.state).toBe('dirty');
    await act(async () => { await vi.advanceTimersByTimeAsync(699); });
    expect(setCharter).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(result.current.read('trellis')?.state).toBe('saving');
    expect(setCharter).toHaveBeenCalledWith('trellis', '# Two\n', 'one');

    await act(async () => save.resolve({ member: 'trellis', conflict: false, charter: { content: '# Two\n', token: 'two' } }));
    expect(result.current.read('trellis')?.state).toBe('saved');
  });

  it('serializes a pending write and coalesces later typing onto the acknowledged token', async () => {
    vi.useFakeTimers();
    const first = deferred<any>();
    const second = deferred<any>();
    const setCharter = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const getCharter = vi.fn().mockResolvedValue({ member: 'alder', charter: { content: 'A', token: 'a' } });
    const { result } = renderHook(() => useCrewCharterAutosave(
      1,
      getCharter,
      setCharter,
    ));
    await act(async () => { await result.current.load('alder'); });
    act(() => result.current.update('alder', 'B'));
    await act(async () => { await vi.advanceTimersByTimeAsync(700); });
    act(() => result.current.update('alder', 'C'));
    expect(setCharter).toHaveBeenCalledTimes(1);

    await act(async () => first.resolve({ member: 'alder', conflict: false, charter: { content: 'B', token: 'b' } }));
    expect(setCharter).toHaveBeenNthCalledWith(2, 'alder', 'C', 'b');
    expect(result.current.read('alder')?.state).toBe('saving');
    await act(async () => second.resolve({ member: 'alder', conflict: false, charter: { content: 'C', token: 'c' } }));
    expect(result.current.read('alder')?.state).toBe('saved');
  });

  it('keeps a reverted draft pending until the in-flight write and corrective write settle', async () => {
    const first = deferred<any>();
    const second = deferred<any>();
    const setCharter = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const getCharter = vi.fn().mockResolvedValue({ member: 'alder', charter: { content: 'A', token: 'a' } });
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));
    await act(async () => { await result.current.load('alder'); });

    act(() => result.current.update('alder', 'B'));
    let firstFlush!: Promise<boolean>;
    act(() => { firstFlush = result.current.flush('alder'); });
    await act(async () => {});
    act(() => result.current.update('alder', 'A'));
    expect(result.current.read('alder')?.state).toBe('saving');

    await act(async () => first.resolve({ member: 'alder', conflict: false, charter: { content: 'B', token: 'b' } }));
    expect(setCharter).toHaveBeenNthCalledWith(2, 'alder', 'A', 'b');
    expect(result.current.read('alder')?.state).toBe('saving');
    await act(async () => second.resolve({ member: 'alder', conflict: false, charter: { content: 'A', token: 'a2' } }));
    await expect(firstFlush).resolves.toBe(true);
    expect(result.current.read('alder')).toMatchObject({ state: 'saved', draft: 'A', acknowledged: { token: 'a2' } });
  });

  it('does not let a read started before a write replace the acknowledged write result', async () => {
    const staleRead = deferred<any>();
    const getCharter = vi.fn()
      .mockResolvedValueOnce({ member: 'trellis', charter: { content: 'A', token: 'a' } })
      .mockReturnValueOnce(staleRead.promise);
    const setCharter = vi.fn().mockResolvedValue({
      member: 'trellis', conflict: false, charter: { content: 'B', token: 'b' },
    });
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));
    await act(async () => { await result.current.load('trellis'); });

    let load!: Promise<void>;
    act(() => { load = result.current.load('trellis', true); });
    act(() => result.current.update('trellis', 'B'));
    await act(async () => { await result.current.flush('trellis'); });
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: 'B', acknowledged: { token: 'b' } });

    await act(async () => staleRead.resolve({ member: 'trellis', charter: { content: 'A', token: 'a' } }));
    await load;
    expect(result.current.read('trellis')).toMatchObject({ state: 'saved', draft: 'B', acknowledged: { token: 'b' } });
  });

  it('coalesces overlapping flushes without turning a failed write into an implicit retry', async () => {
    const save = deferred<any>();
    const setCharter = vi.fn().mockReturnValue(save.promise);
    const getCharter = vi.fn().mockResolvedValue({ member: 'keel', charter: { content: 'A', token: 'a' } });
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));
    await act(async () => { await result.current.load('keel'); });
    act(() => result.current.update('keel', 'B'));

    let first!: Promise<boolean>;
    let second!: Promise<boolean>;
    act(() => {
      first = result.current.flush('keel');
      second = result.current.flush('keel');
    });
    expect(first).toBe(second);
    await act(async () => save.reject(new Error('connection lost')));
    await expect(Promise.all([first, second])).resolves.toEqual([false, false]);
    expect(setCharter).toHaveBeenCalledTimes(1);
    expect(result.current.read('keel')).toMatchObject({ state: 'error', draft: 'B', error: 'connection lost' });
  });

  it('requires a same-content server fence before settling a reverted uncertain write', async () => {
    const confirmation = deferred<any>();
    const setCharter = vi.fn()
      .mockRejectedValueOnce(new Error('save response was lost'))
      .mockReturnValueOnce(confirmation.promise);
    const getCharter = vi.fn().mockResolvedValue({ member: 'alder', charter: { content: 'A', token: '1:a' } });
    const { result } = renderHook(() => useCrewCharterAutosave(1, getCharter, setCharter));
    await act(async () => { await result.current.load('alder'); });

    act(() => result.current.update('alder', 'B'));
    await act(async () => { await result.current.flush('alder'); });
    expect(result.current.read('alder')).toMatchObject({ state: 'error', uncertain: true });

    act(() => result.current.update('alder', 'A'));
    expect(result.current.read('alder')).toMatchObject({ state: 'dirty', uncertain: true, draft: 'A' });
    let retry!: Promise<boolean>;
    act(() => { retry = result.current.flush('alder'); });
    await act(async () => {});
    expect(setCharter).toHaveBeenLastCalledWith('alder', 'A', '1:a');
    expect(result.current.read('alder')?.state).toBe('saving');

    await act(async () => confirmation.resolve({
      member: 'alder', conflict: false, charter: { content: 'A', token: '2:a' },
    }));
    await expect(retry).resolves.toBe(true);
    expect(result.current.read('alder')).toMatchObject({
      state: 'saved', uncertain: false, acknowledged: { token: '2:a' },
    });
  });

  it('keeps local text on conflict and makes both recovery choices explicit', async () => {
    const setCharter = vi.fn()
      .mockResolvedValueOnce({ member: 'keel', conflict: true, charter: { content: 'external', token: 'external-token' } })
      .mockResolvedValueOnce({ member: 'keel', conflict: false, charter: { content: 'mine', token: 'mine-token' } });
    const getCharter = vi.fn().mockResolvedValue({ member: 'keel', charter: { content: 'old', token: 'old-token' } });
    const { result } = renderHook(() => useCrewCharterAutosave(
      1,
      getCharter,
      setCharter,
    ));
    await act(async () => { await result.current.load('keel'); });
    act(() => result.current.update('keel', 'mine'));
    await act(async () => { await result.current.flush('keel'); });
    expect(result.current.read('keel')).toMatchObject({ state: 'conflict', draft: 'mine', external: { content: 'external' } });

    await act(async () => { await result.current.keepMine('keel'); });
    expect(setCharter).toHaveBeenLastCalledWith('keel', 'mine', 'external-token');
    expect(result.current.read('keel')).toMatchObject({ state: 'saved', draft: 'mine' });
  });

  it('re-reads and retries retained text after reconnect without accepting a stale file', async () => {
    const setCharter = vi.fn()
      .mockRejectedValueOnce(new Error('WebSocket not connected'))
      .mockResolvedValueOnce({ member: 'trellis', conflict: false, charter: { content: 'local', token: 'saved' } });
    const getCharter = vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: 'disk', token: 'disk-token' } });
    const { result, rerender } = renderHook(
      ({ generation }) => useCrewCharterAutosave(generation, getCharter, setCharter),
      { initialProps: { generation: 1 } },
    );
    await act(async () => { await result.current.load('trellis'); });
    act(() => result.current.update('trellis', 'local'));
    await act(async () => { await result.current.flush('trellis'); });
    expect(result.current.read('trellis')?.state).toBe('error');

    await act(async () => rerender({ generation: 2 }));
    await act(async () => {});
    expect(setCharter).toHaveBeenLastCalledWith('trellis', 'local', 'disk-token');
    expect(result.current.read('trellis')?.state).toBe('saved');
  });
});
