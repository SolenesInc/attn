import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openActionMenu, openSession } from './test/appFixtures';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

function listedActions() {
  return within(screen.getByRole('dialog', { name: 'Action menu' }))
    .getAllByRole('option')
    .map((option) => option.querySelector('strong')?.textContent);
}

describe('App action menu', () => {
  it('lists only the actions a search matches and runs the highlighted one on Enter', async () => {
    const { daemon } = await renderApp();
    const search = await openActionMenu(daemon);

    fireEvent.change(search, { target: { value: 'countdown' } });
    expect(listedActions()).toEqual(['Turn on auto-settle']);

    fireEvent.keyDown(search, { key: 'Enter' });
    await daemon.idle();

    expect(screen.queryByRole('dialog', { name: 'Action menu' })).toBeNull();
    expect(daemon.sentOf('set_setting')).toEqual([{ cmd: 'set_setting', key: 'auto_settle_enabled', value: 'true' }]);
  });

  it('runs the action the arrow keys moved to', async () => {
    const { daemon } = await renderApp();
    const search = await openActionMenu(daemon);

    fireEvent.change(search, { target: { value: 'queue' } });
    expect(listedActions()).toEqual(['Turn on the agent queue', 'Turn on auto-settle']);

    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'Enter' });
    await daemon.idle();

    expect(daemon.sentOf('set_setting')).toEqual([{ cmd: 'set_setting', key: 'auto_settle_enabled', value: 'true' }]);
  });

  it('leaves focus where an action put it, and gives it back to the terminal when dismissed', async () => {
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1', { state: 'idle' })], workspaces: [agentWorkspace('s1')] },
    });
    await openSession(daemon, 's1');
    const search = await openActionMenu(daemon);
    fireEvent.change(search, { target: { value: 'customize shortcuts' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    await act(() => vi.advanceTimersToNextFrame());
    await daemon.idle();
    const editor = screen.getByRole('dialog', { name: 'Customize Shortcuts' });
    expect(editor).toContainElement(document.activeElement as HTMLElement);
    fireEvent.click(within(editor).getByRole('button', { name: 'Done' }));
    await daemon.idle();

    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    terminal.focus();
    const reopened = await openActionMenu(daemon);
    expect(reopened).toHaveFocus();
    fireEvent.keyDown(reopened, { key: 'Escape' });
    await daemon.idle();

    expect(terminal).toHaveFocus();
  });
});
