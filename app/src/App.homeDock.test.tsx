import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

describe('App dock on home', () => {
  it('opens Automations on an empty dashboard and closes with Escape', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [], desktops: [] } });
    daemon.on('automation_definitions_get', () => ({ event: 'automation_definitions_result', success: true, definitions: [] }));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show Automations' })));

    expect(screen.getByTestId('automations-panel-empty')).toBeVisible();
    expect(daemon.sentOf('automation_definitions_get')).toEqual([
      { cmd: 'automation_definitions_get', request_id: expect.any(String) },
    ]);

    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    expect(screen.getByRole('dialog', { name: 'Commands' })).toBeVisible();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' }));
    expect(screen.queryByRole('dialog', { name: 'Commands' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Hide Automations' })).toBeInTheDocument();

    await gesture(daemon, () => pressShortcut('dock.attention'));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' }));
    expect(document.querySelector('.dock-panel--attention')?.closest('.side-panel-shell')).not.toHaveClass('is-open');
    expect(screen.getByRole('button', { name: 'Hide Automations' })).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' }));

    expect(screen.getByRole('button', { name: 'Show Automations' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Close automations' })).toBeNull();
    expect(daemon.sentOf('automation_definitions_get')).toHaveLength(1);
  });
});
