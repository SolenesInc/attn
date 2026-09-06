import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createSettingsAutosave, SettingsAutosaveProvider, SettingsAutosaveStatus, useAutosaveSetting } from './SettingsAutosave';

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe('settings autosave', () => {
  it('serializes writes to the same setting and retains edits made during acknowledgement', async () => {
    const first = deferred();
    const save = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
    const store = createSettingsAutosave(save);
    store.ensure('model', 'initial');
    store.set('model', 'first');
    const pending = store.commit('model');
    store.set('model', 'second');
    const latest = store.commit('model');
    store.sync('model', 'first');
    expect(store.fields.get('model')?.value).toBe('second');
    expect(save.mock.calls).toEqual([['model', 'first']]);
    first.resolve();
    expect(await pending).toBe(true);
    expect(await latest).toBe(true);
    expect(save.mock.calls).toEqual([['model', 'first'], ['model', 'second']]);
    expect(store.fields.get('model')?.saved).toBe('second');
  });

  it('does not commit text typed during a pending write until requested', async () => {
    const first = deferred();
    const save = vi.fn(() => first.promise);
    const store = createSettingsAutosave(save);
    store.ensure('model', 'initial');
    store.set('model', 'first');
    const pending = store.commit('model');
    store.set('model', 'still typing');
    first.resolve();
    await pending;
    store.sync('model', 'first');
    expect(save).toHaveBeenCalledTimes(1);
    expect(store.fields.get('model')?.value).toBe('still typing');
  });

  it('flushes dirty registered fields after their controls have left the page', async () => {
    const save = vi.fn();
    const store = createSettingsAutosave(save);
    store.ensure('chief_model_claude', '');
    store.ensure('default_model_codex', '');
    store.set('chief_model_claude', 'sonnet');
    expect(await store.flush()).toBe(true);
    expect(save.mock.calls).toEqual([['chief_model_claude', 'sonnet']]);
  });

  it('blocks a flush on validation failure and retains the invalid draft', async () => {
    const save = vi.fn();
    const store = createSettingsAutosave(save);
    store.ensure('intervals', 'valid').serialize = () => { throw new Error('Enter a whole number of seconds'); };
    store.set('intervals', 'invalid');
    expect(await store.flush()).toBe(false);
    expect(save).not.toHaveBeenCalled();
    expect(store.fields.get('intervals')?.value).toBe('invalid');
  });

  it('shows confirmation only after acknowledgement and keeps a failed edit retryable', async () => {
    const first = deferred();
    const save = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
    function Input() {
      const draft = useAutosaveSetting('model', 'initial', save);
      return <input aria-label="Model" value={draft.value} onChange={draft.onChange} onBlur={draft.onBlur} />;
    }
    render(<SettingsAutosaveProvider save={save}><Input /><SettingsAutosaveStatus /></SettingsAutosaveProvider>);
    fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'new model' } });
    fireEvent.blur(screen.getByLabelText('Model'));
    expect(screen.getByText('Saving…')).toBeInTheDocument();
    expect(screen.queryByText('Saved')).toBeNull();
    await act(async () => { first.reject(new Error('Connection lost')); });
    expect(screen.getByRole('alert')).toHaveTextContent('Connection lost');
    expect(screen.getByLabelText('Model')).toHaveValue('new model');
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Retry' })); });
    expect(save.mock.calls).toEqual([['model', 'new model'], ['model', 'new model']]);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByText('Saved')).toBeInTheDocument();
  });
});
