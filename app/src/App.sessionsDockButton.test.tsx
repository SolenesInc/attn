import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

function sessionsButton() {
  return screen.getByRole('button', { name: /^Open Sessions \(/ });
}

function worktreesButton() {
  return screen.getByRole('button', { name: 'Open Worktrees' });
}

function ledger() {
  return screen.queryByRole('dialog', { name: 'Sessions and worktrees' });
}

function shownList() {
  const panel = ledger();
  if (!panel) return null;
  const tabs = within(panel).getByRole('navigation', { name: 'Which list' });
  return within(tabs).getByRole('button', { current: 'page' }).textContent;
}

function pressSessionsKey() {
  pressShortcut('sessions.open');
}

describe('sessions dock button', () => {
  it('opens the sessions panel and tracks its open state', async () => {
    await renderApp();
    expect(sessionsButton()).not.toHaveClass('active');
    expect(ledger()).toBeNull();

    fireEvent.click(sessionsButton());

    expect(shownList()).toBe('Sessions');
    expect(sessionsButton()).toHaveClass('active');

    fireEvent.click(within(ledger()!).getByRole('button', { name: /^Close ?esc$/i }));

    expect(ledger()).toBeNull();
    expect(sessionsButton()).not.toHaveClass('active');
  });

  it('sits next to the worktrees button, which opens the same surface on its other list', async () => {
    await renderApp();

    const buttons = screen.getAllByRole('button');
    expect(buttons.indexOf(worktreesButton())).toBe(buttons.indexOf(sessionsButton()) + 1);

    fireEvent.click(worktreesButton());

    expect(shownList()).toBe('Worktrees');
    expect(worktreesButton()).toHaveClass('active');
    expect(sessionsButton()).not.toHaveClass('active');
  });

  it('the sessions key closes whichever list is up and opens on Sessions', async () => {
    await renderApp();
    fireEvent.click(worktreesButton());
    expect(shownList()).toBe('Worktrees');

    pressSessionsKey();
    expect(ledger()).toBeNull();

    pressSessionsKey();
    expect(shownList()).toBe('Sessions');
  });

  it('slides a closing dock panel out from where it sat, beside a panel opened after it', async () => {
    const { daemon } = await renderApp();
    const offset = (className: string) => document.querySelector<HTMLElement>(`.${className}`)!.style.getPropertyValue('--side-panel-offset');
    const isOpen = (className: string) => document.querySelector(`.${className}`)!.closest('.side-panel-shell')!.classList.contains('is-open');

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show Automations' })));
    await gesture(daemon, () => pressShortcut('dock.attention'));
    expect(offset('dock-panel--attention')).toBe('clamp(420px, 42vw, 640px)');

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Hide Automations' })));

    expect(isOpen('dock-panel--automations')).toBe(false);
    expect(offset('dock-panel--automations')).toBe('0px');
    expect(offset('dock-panel--attention')).toBe('0px');

    await act(() => vi.advanceTimersByTimeAsync(260));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show Automations' })));

    expect(offset('dock-panel--automations')).toBe('clamp(360px, 48vw, 600px)');
    expect(isOpen('dock-panel--attention')).toBe(true);
  });
});
