import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

function renderSessions() {
  return renderApp({
    initialState: {
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
      desktops: [soloDesktop('s1'), soloDesktop('s2')],
    },
  });
}

describe('App terminal focus', () => {
  it('gives the opened session’s terminal the keyboard once it is ready, when nothing else holds it', async () => {
    const { daemon } = await renderSessions();

    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    (document.activeElement as HTMLElement).blur();
    await daemon.idle();

    expect(screen.getByRole('textbox', { name: 'Terminal input' })).toHaveFocus();
  });
});
