
import { useCallback, useReducer } from 'react';
import type { DaemonEndpoint } from '../hooks/useDaemonSocket';
import { BUILD_PROFILE } from '../utils/buildProfile';
import { usePanelAction, type PanelAction } from './settingsPanelAction';

export interface EndpointFields {
  name: string;
  target: string;
  profile: string;
}

interface EndpointFormState {
  draft: EndpointFields;
  editing: (EndpointFields & { id: string }) | null;
}

type EndpointFormEvent =
  | { type: 'draft'; field: keyof EndpointFields; value: string }
  | { type: 'draft-cleared' }
  | { type: 'edit-begun'; endpoint: DaemonEndpoint }
  | { type: 'edit'; field: keyof EndpointFields; value: string }
  | { type: 'edit-cancelled' }
  | { type: 'reopened' };

const emptyDraft: EndpointFields = { name: '', target: '', profile: BUILD_PROFILE };

function reduce(state: EndpointFormState, event: EndpointFormEvent): EndpointFormState {
  switch (event.type) {
    case 'draft':
      return { ...state, draft: { ...state.draft, [event.field]: event.value } };
    case 'draft-cleared':
      return { ...state, draft: emptyDraft };
    case 'edit-begun':
      return {
        ...state,
        editing: {
          id: event.endpoint.id,
          name: event.endpoint.name,
          target: event.endpoint.ssh_target,
          profile: event.endpoint.profile || '',
        },
      };
    case 'edit':
      return state.editing
        ? { ...state, editing: { ...state.editing, [event.field]: event.value } }
        : state;
    case 'edit-cancelled':
      return { ...state, editing: null };
    case 'reopened':
      if (state.editing === null && state.draft.name === '' && state.draft.target === '') {
        return state;
      }
      return { draft: { ...state.draft, name: '', target: '' }, editing: null };
  }
}

export interface EndpointPanel extends PanelAction {
  draft: EndpointFields;
  setDraft: (field: keyof EndpointFields, value: string) => void;
  clearDraft: () => void;
  editing: EndpointFormState['editing'];
  beginEdit: (endpoint: DaemonEndpoint) => void;
  setEdit: (field: keyof EndpointFields, value: string) => void;
  cancelEdit: () => void;
  reopen: () => void;
}

// Every returned callback is stable: `reopen` is an effect dependency in
// SettingsModal, and a fresh identity per render would run that effect every render.
export function useEndpointPanel(): EndpointPanel {
  const [form, dispatch] = useReducer(reduce, { draft: emptyDraft, editing: null });
  const action = usePanelAction();
  const { clearError } = action;

  const beginEdit = useCallback((endpoint: DaemonEndpoint) => {
    clearError();
    dispatch({ type: 'edit-begun', endpoint });
  }, [clearError]);

  const setDraft = useCallback((field: keyof EndpointFields, value: string) => {
    dispatch({ type: 'draft', field, value });
  }, []);
  const clearDraft = useCallback(() => dispatch({ type: 'draft-cleared' }), []);
  const setEdit = useCallback((field: keyof EndpointFields, value: string) => {
    dispatch({ type: 'edit', field, value });
  }, []);
  const cancelEdit = useCallback(() => dispatch({ type: 'edit-cancelled' }), []);
  const reopen = useCallback(() => dispatch({ type: 'reopened' }), []);

  return {
    ...action,
    draft: form.draft,
    setDraft,
    clearDraft,
    editing: form.editing,
    beginEdit,
    setEdit,
    cancelEdit,
    reopen,
  };
}
