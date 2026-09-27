import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openActionMenu, openSession } from './test/appFixtures';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

async function openContextCap(contextWindowCap?: number) {
  const { daemon } = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { label: 'trellis', state: 'idle', context_window_cap: contextWindowCap })],
      desktops: [soloDesktop('s1')],
    },
  });
  await openSession(daemon, 'trellis');
  const search = await openActionMenu(daemon);
  fireEvent.change(search, { target: { value: '>context window' } });
  await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
  return daemon;
}

const capPrompt = () => screen.queryByRole('dialog', { name: 'Context window cap' });

async function saveCap(daemon: ScriptedDaemon, value: string) {
  fireEvent.change(within(capPrompt()!).getByLabelText('Context window cap in tokens'), { target: { value } });
  await gesture(daemon, () => fireEvent.click(within(capPrompt()!).getByRole('button', { name: 'Save' })));
}

function listedActions() {
  return within(screen.getByRole('dialog', { name: 'Commands' }))
    .getAllByRole('option')
    .map((option) => option.querySelector('.unified-palette-name')?.firstChild?.textContent);
}

describe('App action menu', () => {
  it('lists only the actions a search matches and runs the highlighted one on Enter', async () => {
    const { daemon } = await renderApp();
    const search = await openActionMenu(daemon);

    fireEvent.change(search, { target: { value: '>countdown' } });
    expect(listedActions()).toEqual(['Turn on auto-settle']);

    fireEvent.keyDown(search, { key: 'Enter' });
    await daemon.idle();

    expect(screen.queryByRole('dialog', { name: 'Commands' })).toBeNull();
    expect(daemon.sentOf('set_setting')).toEqual([{ cmd: 'set_setting', key: 'auto_settle_enabled', value: 'true' }]);
  });

  it('runs the action the arrow keys moved to', async () => {
    const { daemon } = await renderApp();
    const search = await openActionMenu(daemon);

    fireEvent.change(search, { target: { value: '>turn on' } });
    expect(listedActions()).toEqual(['Turn on the agent queue', 'Turn on auto-settle', 'Jump to the oldest turn']);

    fireEvent.keyDown(search, { key: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'Enter' });
    await daemon.idle();

    expect(daemon.sentOf('set_setting')).toEqual([{ cmd: 'set_setting', key: 'auto_settle_enabled', value: 'true' }]);
  });

  it('leaves focus where an action put it, and gives it back to the terminal when dismissed', async () => {
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1', { state: 'idle' })], desktops: [soloDesktop('s1')] },
    });
    await openSession(daemon, 's1');
    const search = await openActionMenu(daemon);
    fireEvent.change(search, { target: { value: '>customize shortcuts' } });
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

  describe('capping a session’s context window', () => {
    it.each([
      ['a new cap as a number', '800000', 800000],
      ['a blank cap as 0, clearing it', '', 0],
    ])('starts from the current cap and sends %s', async (_, typed, cap) => {
      const daemon = await openContextCap(300000);
      daemon.on('set_session_context_window_cap', ({ session_id }) => ({ event: 'session_context_window_cap_result', session_id, cap, success: true }));
      expect(within(capPrompt()!).getByLabelText('Context window cap in tokens')).toHaveValue(300000);

      await saveCap(daemon, typed);

      expect(daemon.sentOf('set_session_context_window_cap')).toEqual([{ cmd: 'set_session_context_window_cap', session_id: 's1', cap }]);
      expect(capPrompt()).toBeNull();
    });

    it('sends nothing for an unchanged cap or a fractional one', async () => {
      const daemon = await openContextCap(300000);
      await saveCap(daemon, '300000');
      expect(capPrompt()).toBeNull();

      const search = await openActionMenu(daemon);
      fireEvent.change(search, { target: { value: '>context window' } });
      await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
      await saveCap(daemon, '1.5');

      expect(within(capPrompt()!).getByText('Enter a whole number of tokens, or leave blank for no cap')).toBeInTheDocument();
      expect(daemon.sentOf('set_session_context_window_cap')).toEqual([]);
    });

    it('keeps the prompt open with the daemon’s reason when it refuses the cap', async () => {
      const refusal = 'context window cap must be 0 (no cap) or between 10000 and 2000000 tokens; got 5';
      const daemon = await openContextCap();
      daemon.on('set_session_context_window_cap', ({ session_id, cap }) => ({ event: 'session_context_window_cap_result', session_id, cap, success: false, error: refusal }));

      await saveCap(daemon, '5');

      expect(within(capPrompt()!).getByText(refusal)).toBeInTheDocument();
    });
  });

  it('holds app shortcuts while open, and opens the attention drawer on the sessions waiting for the user', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { state: 'working' }), daemonSession('s2', { state: 'waiting_input' }), daemonSession('s3', { state: 'idle' })],
        desktops: ['s1', 's2', 's3'].map((id) => soloDesktop(id)),
      },
    });
    const drawer = screen.getByText('Needs Attention').closest('aside')!;
    await openActionMenu(daemon);
    expect(drawer).toHaveAttribute('aria-hidden', 'true');

    pressShortcut('session.new');
    await daemon.idle();
    expect(screen.queryByTestId('location-picker-overlay')).toBeNull();

    fireEvent.mouseDown(screen.getByText('Open attention drawer'));
    await daemon.idle();

    expect(screen.queryByRole('dialog', { name: 'Commands' })).toBeNull();
    expect(drawer).toHaveAttribute('aria-hidden', 'false');
    expect([...drawer.querySelectorAll('[data-testid^="attention-session-"]')].map((item) => item.getAttribute('data-testid'))).toEqual(['attention-session-s2']);
  });
});
