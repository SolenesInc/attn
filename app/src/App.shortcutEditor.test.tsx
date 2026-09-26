import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu } from './test/appFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';

async function openShortcutEditor(daemon: ScriptedDaemon) {
  const search = await openActionMenu(daemon);
  fireEvent.change(search, { target: { value: 'customize shortcuts' } });
  fireEvent.keyDown(search, { key: 'Enter' });
  await daemon.idle();
  return screen.getByRole('dialog', { name: 'Customize Shortcuts' });
}

function actionMenuRow(editor: HTMLElement) {
  return within(editor).getByText('Action menu').closest<HTMLElement>('.shortcut-editor-row')!;
}

async function press(daemon: ScriptedDaemon, init: KeyboardEventInit) {
  fireEvent.keyDown(window, init);
  await daemon.idle();
}

async function renderEditor() {
  const view = await renderApp();
  serveSettings(view.daemon);
  const editor = await openShortcutEditor(view.daemon);
  return { ...view, editor, row: actionMenuRow(editor) };
}

describe('App shortcut editor', () => {
  it('rebinds a shortcut to the combo pressed while recording, and the new combo runs it', async () => {
    const { daemon, editor, row } = await renderEditor();

    fireEvent.click(within(row).getByTitle('Click to rebind'));
    await press(daemon, { key: 'm', metaKey: true });

    expect(within(row).getByTitle('Click to rebind')).toHaveTextContent('⌘M');
    expect(JSON.parse(daemon.sentOf('set_setting').slice(-1)[0]!.value)).toMatchObject({
      overrides: { 'ui.actionMenu': { key: 'm', meta: true } },
    });

    fireEvent.click(within(editor).getByRole('button', { name: 'Done' }));
    await daemon.idle();
    await press(daemon, { key: 'm', metaKey: true });
    expect(screen.getByRole('dialog', { name: 'Action menu' })).toBeInTheDocument();
  });

  it('refuses a chord leader without a modifier and leaves the binding alone', async () => {
    const { daemon, row } = await renderEditor();

    fireEvent.click(within(row).getByRole('button', { name: 'Record a chord' }));
    await press(daemon, { key: 'a' });

    expect(row).toHaveTextContent('A chord leader needs a ⌘ or ⌥ modifier.');
    expect(daemon.sentOf('set_setting')).toEqual([]);
  });

  it('stops recording on Escape and keeps the previous binding', async () => {
    const { daemon, row } = await renderEditor();

    fireEvent.click(within(row).getByTitle('Click to rebind'));
    await press(daemon, { key: 'Escape' });

    expect(within(row).getByTitle('Click to rebind')).toHaveTextContent('⌘K');
    expect(daemon.sentOf('set_setting')).toEqual([]);
  });
});
