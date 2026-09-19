import { Suspense, startTransition, useEffect, useState } from 'react';
import { act, render, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useAutosave, type AutosaveResult, type AutosaveSpec } from './useAutosave';

interface Doc { content: string; version: number }

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

const doc = (content: string, version: number): Doc => ({ content, version });

function spec(send: AutosaveSpec<string, Doc>['send']): AutosaveSpec<string, Doc> {
  return {
    send,
    equal: (draft, ack) => draft === ack.content,
    settled: (ack) => ack.content,
    stale: (candidate, current) => candidate.version < current.version,
  };
}

function setup(send: AutosaveSpec<string, Doc>['send'], connectionGeneration = 0) {
  const hook = renderHook(
    ({ generation }) => useAutosave(spec(send), generation),
    { initialProps: { generation: connectionGeneration } },
  );
  act(() => hook.result.current.adopt('doc', doc('A', 1)));
  return hook;
}

const NEVER = new Promise<never>(() => {});

function SuspendWhen({ when }: { when: boolean }) {
  if (when) throw NEVER;
  return null;
}

describe('useAutosave', () => {
  it('keeps the committed sender when React discards a render', async () => {
    const senderA = vi.fn(() => NEVER);
    const senderB = vi.fn(() => NEVER);
    let committed: ReturnType<typeof useAutosave<string, Doc>> | null = null;
    let renderSenderB: (() => void) | null = null;

    function Harness() {
      const [send, setSend] = useState(() => senderA);
      const [suspend, setSuspend] = useState(false);
      const autosave = useAutosave(spec(send), 0);

      useEffect(() => {
        committed = autosave;
        renderSenderB = () => {
          startTransition(() => {
            setSend(() => senderB);
            setSuspend(true);
          });
        };
      });

      return <SuspendWhen when={suspend} />;
    }

    render(
      <Suspense fallback={null}>
        <Harness />
      </Suspense>,
    );
    await act(async () => {});
    act(() => committed?.adopt('doc', doc('A', 1)));

    await act(async () => {
      renderSenderB?.();
    });
    await act(async () => {
      committed?.update('doc', 'B');
      void committed?.pump('doc');
    });

    expect(senderA).toHaveBeenCalledOnce();
    expect(senderB).not.toHaveBeenCalled();
  });

  it('re-sends a draft edited while a write was out and settles only on the final acknowledgement', async () => {
    const first = deferred<AutosaveResult<Doc>>();
    const second = deferred<AutosaveResult<Doc>>();
    const send = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const { result } = setup(send);

    act(() => result.current.update('doc', 'B'));
    let flush!: Promise<boolean>;
    act(() => { flush = result.current.pump('doc'); });
    expect(result.current.pump('doc')).toBe(flush);
    act(() => result.current.update('doc', 'C'));
    expect(send).toHaveBeenCalledExactlyOnceWith('doc', 'B', doc('A', 1));
    expect(result.current.read('doc')).toMatchObject({ state: 'saving', draft: 'C' });

    await act(async () => first.resolve({ ack: doc('B', 2) }));
    expect(send).toHaveBeenNthCalledWith(2, 'doc', 'C', doc('B', 2));
    expect(result.current.read('doc')).toMatchObject({ state: 'saving', draft: 'C', acknowledged: doc('B', 2) });

    await act(async () => second.resolve({ ack: doc('C', 3) }));
    await expect(flush).resolves.toBe(true);
    expect(result.current.read('doc')).toMatchObject({ state: 'saved', draft: 'C', acknowledged: doc('C', 3) });
  });

  it('ignores a write result older than an acknowledgement observed meanwhile', async () => {
    const first = deferred<AutosaveResult<Doc>>();
    const send = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({ ack: doc('C', 7) });
    const { result } = setup(send);

    act(() => result.current.update('doc', 'B'));
    act(() => { void result.current.pump('doc'); });
    act(() => result.current.update('doc', 'C'));
    act(() => result.current.adopt('doc', doc('external', 6)));
    await act(async () => first.resolve({ ack: doc('B', 2) }));

    expect(send).toHaveBeenNthCalledWith(2, 'doc', 'C', doc('external', 6));
    expect(result.current.read('doc')).toMatchObject({ state: 'saved', acknowledged: doc('C', 7) });
  });

  it('forces the next write after a lost response even when the draft matches the acknowledgement', async () => {
    const send = vi.fn()
      .mockRejectedValueOnce(new Error('save response was lost'))
      .mockResolvedValueOnce({ error: 'invalid content' })
      .mockResolvedValueOnce({ ack: doc('A', 2) });
    const { result } = setup(send);

    act(() => result.current.update('doc', 'B'));
    await act(async () => { await result.current.pump('doc'); });
    expect(result.current.read('doc')).toMatchObject({ state: 'error', error: 'save response was lost', uncertain: true });

    act(() => result.current.update('doc', 'A'));
    expect(result.current.read('doc')).toMatchObject({ state: 'dirty', uncertain: true });
    await act(async () => { await result.current.pump('doc'); });
    expect(send).toHaveBeenNthCalledWith(2, 'doc', 'A', doc('A', 1));
    expect(result.current.read('doc')).toMatchObject({ state: 'error', error: 'invalid content', uncertain: true });

    act(() => result.current.adopt('doc', doc('A', 1)));
    expect(result.current.read('doc')?.state).toBe('error');

    await act(async () => { await result.current.pump('doc'); });
    expect(send).toHaveBeenNthCalledWith(3, 'doc', 'A', doc('A', 1));
    expect(result.current.read('doc')).toMatchObject({ state: 'saved', uncertain: false, acknowledged: doc('A', 2) });
  });

  it('holds a conflict as data until the user keeps their edit or takes the external version', async () => {
    const send = vi.fn()
      .mockResolvedValueOnce({ conflict: doc('external', 5) })
      .mockResolvedValueOnce({ ack: doc('mine', 6) })
      .mockResolvedValueOnce({ conflict: doc('external', 5) });
    const { result } = setup(send);

    act(() => result.current.update('doc', 'mine'));
    await act(async () => { await result.current.pump('doc'); });
    expect(result.current.read('doc')).toMatchObject({
      state: 'conflict', draft: 'mine', acknowledged: doc('A', 1), conflict: doc('external', 5),
    });
    await expect(result.current.pump('doc')).resolves.toBe(false);
    expect(send).toHaveBeenCalledTimes(1);

    await act(async () => { await result.current.keepMine('doc'); });
    expect(send).toHaveBeenNthCalledWith(2, 'doc', 'mine', doc('external', 5));
    expect(result.current.read('doc')).toMatchObject({ state: 'saved', draft: 'mine', acknowledged: doc('mine', 6) });

    act(() => result.current.update('doc', 'later'));
    await act(async () => { await result.current.pump('doc'); });
    act(() => result.current.useExternal('doc'));
    expect(result.current.read('doc')).toMatchObject({
      state: 'saved', draft: 'external', acknowledged: doc('external', 5), conflict: undefined,
    });
  });

  it('retries a transport failure after reconnect but leaves other failures to the user', async () => {
    const send = vi.fn()
      .mockRejectedValueOnce(new Error('WebSocket not connected'))
      .mockRejectedValueOnce(new Error('disk is read-only'))
      .mockResolvedValue({ ack: doc('B', 2) });
    const { result, rerender } = setup(send, 1);

    act(() => result.current.update('doc', 'B'));
    await act(async () => { await result.current.pump('doc'); });
    expect(result.current.read('doc')?.state).toBe('error');

    await act(async () => rerender({ generation: 2 }));
    expect(send).toHaveBeenCalledTimes(2);
    expect(result.current.read('doc')).toMatchObject({ state: 'error', error: 'disk is read-only' });

    await act(async () => rerender({ generation: 3 }));
    expect(send).toHaveBeenCalledTimes(2);
  });
});
