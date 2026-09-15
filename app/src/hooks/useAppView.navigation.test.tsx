import { useLayoutEffect } from 'react';
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useAppView } from './useAppView';

describe('app view navigation', () => {
  it('selects a newly active session and clears follow before committing', () => {
    const committed: { view: string; follow: boolean }[] = [];
    const { result, rerender } = renderHook(
      ({ sessionId }: { sessionId: string | null }) => {
        const state = useAppView(sessionId);
        useLayoutEffect(() => {
          committed.push({ view: state.view, follow: state.followNextTurn });
        }, [state.view, state.followNextTurn]);
        return state;
      },
      { initialProps: { sessionId: null as string | null } },
    );
    act(() => result.current.setFollowNextTurn(true));
    committed.length = 0;
    rerender({ sessionId: 'agent' });
    expect(committed).toEqual([{ view: 'session', follow: false }]);
  });

  it('keeps the selected grid until selection actually changes', () => {
    const { result, rerender } = renderHook(useAppView, { initialProps: 'agent' as string | null });
    act(() => result.current.setView('grid'));
    rerender('agent');
    expect(result.current.view).toBe('grid');
    rerender('other-agent');
    expect(result.current.view).toBe('session');
    act(() => result.current.setView('dashboard'));
    rerender(null);
    expect(result.current.view).toBe('dashboard');
  });
});
