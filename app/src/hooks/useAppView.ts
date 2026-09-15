import { useCallback, useReducer, type SetStateAction } from 'react';

type AppView = 'dashboard' | 'session' | 'grid';

interface ViewState {
  view: AppView;
  activeSessionId: string | null;
  followNextTurn: boolean;
}

type ViewAction =
  | { type: 'session'; id: string | null }
  | { type: 'view'; value: SetStateAction<AppView> }
  | { type: 'follow'; value: SetStateAction<boolean> };

function reduceView(state: ViewState, action: ViewAction): ViewState {
  if (action.type === 'follow')
    return {
      ...state,
      followNextTurn:
        typeof action.value === 'function' ? action.value(state.followNextTurn) : action.value,
    };
  const view =
    action.type === 'session'
      ? action.id
        ? 'session'
        : state.view
      : typeof action.value === 'function'
        ? action.value(state.view)
        : action.value;
  return {
    view,
    activeSessionId: action.type === 'session' ? action.id : state.activeSessionId,
    followNextTurn: view === 'dashboard' && state.followNextTurn,
  };
}

export function useAppView(activeSessionId: string | null) {
  const [state, dispatch] = useReducer(reduceView, {
    activeSessionId,
    view: activeSessionId ? 'session' : 'dashboard',
    followNextTurn: false,
  });
  if (state.activeSessionId !== activeSessionId) {
    dispatch({ type: 'session', id: activeSessionId });
  }
  const setView = useCallback(
    (value: SetStateAction<AppView>) => dispatch({ type: 'view', value }),
    [],
  );
  const setFollowNextTurn = useCallback(
    (value: SetStateAction<boolean>) => dispatch({ type: 'follow', value }),
    [],
  );
  return { view: state.view, followNextTurn: state.followNextTurn, setView, setFollowNextTurn };
}
