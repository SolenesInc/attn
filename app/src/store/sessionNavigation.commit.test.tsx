import { useLayoutEffect } from 'react';
import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { useSessionStore } from './sessions';

beforeEach(() => useSessionStore.setState(useSessionStore.getInitialState(), true));

describe('app view navigation', () => {
  it('selects a newly active session and clears follow in one store notification and React commit', () => {
    const committed: { view: string; follow: boolean }[] = [];
    const { result } = renderHook(() => {
      const state = useSessionStore();
      useLayoutEffect(() => {
        committed.push({ view: state.view, follow: state.followNextTurn });
      }, [state.view, state.followNextTurn]);
      return state;
    });
    act(() => result.current.setFollowNextTurn(true));
    committed.length = 0;
    const observed: string[] = [];
    const unsubscribe = useSessionStore.subscribe((state) =>
      observed.push(`${state.activeSessionId}:${state.view}:${state.followNextTurn}`),
    );
    act(() => result.current.setActiveSession('agent'));
    unsubscribe();
    expect(observed).toEqual(['agent:session:false']);
    expect(committed).toEqual([{ view: 'session', follow: false }]);
  });

  it('keeps grid through unrelated data updates and leaves it for explicit selection', () => {
    const { result } = renderHook(() => useSessionStore());
    act(() => {
      result.current.setActiveSession('agent');
      result.current.setView('grid');
    });
    act(() => result.current.syncNavigationSettings({}));
    expect(result.current.view).toBe('grid');
    act(() => result.current.setActiveSession('other-agent'));
    expect(result.current.view).toBe('session');
    act(() => result.current.goToDashboard());
    expect(result.current.view).toBe('dashboard');
  });
});
