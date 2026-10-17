import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { openSession } from './test/appFixtures';
import { gesture, renderApp } from './test/renderApp';

describe('Shell display names', () => {
  it('follows live titles in the sidebar and tile header, with a user name taking precedence', async () => {
    const shell = daemonSession('shell', { agent: 'shell', label: '', title: 'shop', state: 'idle' });
    const { daemon } = await renderApp({ initialState: { sessions: [shell], desktops: [soloDesktop(shell.id)] } });
    await openSession(daemon, 'shop');
    const header = () => within(document.querySelector<HTMLElement>('[data-pane-id="pane-shell"] .desktop-pane-header')!);
    await gesture(daemon, () => fireEvent.click(header().getByRole('button', { name: 'Focus agent shop' })));
    const expectName = (name: string) => {
      expect(screen.getByRole('button', { name: new RegExp(`^Open ${name}`) })).toBeInTheDocument();
      expect(header().getByText(name)).toBeInTheDocument();
      expect(document.querySelector('.desktop-focus-title')).toHaveTextContent(name);
    };
    expectName('shop');
    await gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session: { ...shell, title: 'title-corpus.md - Nvim' } }));
    expectName('title-corpus.md - Nvim');
    await gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session: { ...shell, title: 'warehouse', label: 'my server' } }));
    expectName('my server');
    daemon.on('rename_session', ({ session_id, label }) => {
      daemon.emit({ event: 'session_state_changed', session: { ...shell, title: 'warehouse', label } });
      return { event: 'rename_result', cmd: 'rename_session', id: session_id, success: true };
    });
    fireEvent.click(header().getByRole('button', { name: 'Rename session my server' }));
    const input = screen.getByRole('textbox', { name: 'Rename session' });
    fireEvent.change(input, { target: { value: '' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
    expect(daemon.sentOf('rename_session')).toEqual([{ cmd: 'rename_session', session_id: 'shell', label: '' }]);
    expectName('warehouse');
    await gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session: { ...shell, title: undefined } }));
    expectName('Shell');
  });
});
