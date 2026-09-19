import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useFocusedLeaf } from './useFocusedLeaf';

const leafIds = new Set(['pane-a', 'pane-b', 'tile-readme']);
const agentPanes = new Map([
  ['pane-a', { sessionId: 'session-a' }],
  ['pane-b', { sessionId: 'session-b' }],
]);

function renderFocusedLeaf(selectedSessionId: string | null) {
  return renderHook(
    ({ selected, leaves }: { selected: string | null; leaves: ReadonlySet<string> }) =>
      useFocusedLeaf(leaves, agentPanes, selected),
    { initialProps: { selected: selectedSessionId, leaves: leafIds } },
  );
}

describe('useFocusedLeaf selection', () => {
  it('keeps an agent leaf focused while its session is the selection', () => {
    const hook = renderFocusedLeaf('session-a');
    act(() => hook.result.current[1]('pane-a'));
    hook.rerender({ selected: 'session-a', leaves: leafIds });
    expect(hook.result.current[0]).toBe('pane-a');
  });

  it('clears an agent leaf when another session becomes the selection', () => {
    const hook = renderFocusedLeaf('session-a');
    act(() => hook.result.current[1]('pane-a'));
    hook.rerender({ selected: 'session-b', leaves: leafIds });
    expect(hook.result.current[0]).toBeNull();
  });

  it('clears an agent leaf when the selection is no session at all', () => {
    const hook = renderFocusedLeaf('session-a');
    act(() => hook.result.current[1]('pane-a'));
    hook.rerender({ selected: null, leaves: leafIds });
    expect(hook.result.current[0]).toBeNull();
  });

  it('keeps a tile leaf focused regardless of the selection', () => {
    const hook = renderFocusedLeaf('session-a');
    act(() => hook.result.current[1]('tile-readme'));
    hook.rerender({ selected: null, leaves: leafIds });
    expect(hook.result.current[0]).toBe('tile-readme');
  });

  it('clears a leaf that left the layout', () => {
    const hook = renderFocusedLeaf('session-a');
    act(() => hook.result.current[1]('pane-a'));
    hook.rerender({ selected: 'session-a', leaves: new Set(['pane-b']) });
    expect(hook.result.current[0]).toBeNull();
  });
});
