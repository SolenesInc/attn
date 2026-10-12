import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu, openSession } from './test/appFixtures';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

async function setup() {
  const sessions = [
    daemonSession('s1', { label: 'ledger sweep', state: 'idle' }),
    daemonSession('s2', { label: 'docs sweep', state: 'idle' }),
  ];
  const view = await renderApp({ initialState: { sessions, desktops: sessions.map((session) => soloDesktop(session.id)) } });
  await openSession(view.daemon, 'docs sweep');
  return view;
}

async function rename(daemon: ScriptedDaemon) {
  screen.getByRole('textbox', { name: 'Terminal input' }).focus();
  const search = await openActionMenu(daemon);
  fireEvent.change(search, { target: { value: '>rename' } });
  expect(screen.getByRole('option')).toHaveTextContent('Rename this session');
  await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
  return within(screen.getByRole('dialog', { name: 'Rename session' })).getByRole('textbox') as HTMLInputElement;
}

describe('Palette session rename', () => {
  it('prefills the active session and updates its sidebar and tile through the existing rename request', async () => {
    const { daemon } = await setup();
    daemon.on('rename_session', ({ session_id, label }) => [
      { event: 'session_state_changed', session: daemonSession(session_id, { label, state: 'idle' }) },
      { event: 'rename_result', cmd: 'rename_session', id: session_id, success: true },
    ]);
    const input = await rename(daemon);
    expect(input).toHaveValue('docs sweep');
    expect(input).toHaveFocus();
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, 'docs sweep'.length]);
    fireEvent.change(input, { target: { value: '  nightly docs sweep  ' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(daemon.sentOf('rename_session')).toEqual([{ cmd: 'rename_session', session_id: 's2', label: 'nightly docs sweep' }]);
    expect(screen.queryByRole('dialog', { name: 'Rename session' })).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Terminal input' })).toHaveFocus();
    expect(screen.getByRole('button', { name: 'Open nightly docs sweep' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rename session nightly docs sweep' })).toBeInTheDocument();
  });

  it('cancels on Escape without changing the session', async () => {
    const { daemon } = await setup();
    const input = await rename(daemon);
    fireEvent.change(input, { target: { value: 'cancelled name' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Escape' }));

    expect(screen.queryByRole('dialog', { name: 'Rename session' })).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Terminal input' })).toHaveFocus();
    expect(daemon.sentOf('rename_session')).toEqual([]);
    expect(screen.getByRole('button', { name: 'Open docs sweep' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rename session docs sweep' })).toBeInTheDocument();
  });

  it('shows the daemon’s 48-character limit error and lets the user correct the name', async () => {
    const { daemon } = await setup();
    const refusal = 'name is 49 characters, over the 48-character limit';
    daemon.on('rename_session', ({ session_id }) => ({ event: 'rename_result', cmd: 'rename_session', id: session_id, success: false, error: refusal }));
    const input = await rename(daemon);
    fireEvent.change(input, { target: { value: 'x'.repeat(49) } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
    expect(screen.getByRole('dialog', { name: 'Rename session' })).toHaveTextContent(refusal);
    expect(input).toBeEnabled();

    daemon.on('rename_session', ({ session_id }) => ({ event: 'rename_result', cmd: 'rename_session', id: session_id, success: true }));
    fireEvent.change(input, { target: { value: '🙂'.repeat(48) } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
    expect(daemon.sentOf('rename_session')).toEqual([
      { cmd: 'rename_session', session_id: 's2', label: 'x'.repeat(49) },
      { cmd: 'rename_session', session_id: 's2', label: '🙂'.repeat(48) },
    ]);
    expect(screen.queryByRole('dialog', { name: 'Rename session' })).toBeNull();
  });

  it('offers no session rename on the dashboard', async () => {
    const { daemon } = await renderApp();
    const search = await openActionMenu(daemon);
    fireEvent.change(search, { target: { value: '>rename' } });
    expect(screen.queryByRole('option')).toBeNull();
  });
});
