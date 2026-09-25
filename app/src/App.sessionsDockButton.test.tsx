import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderApp } from './test/renderApp';

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
  fireEvent.keyDown(window, { key: 'L', metaKey: true, shiftKey: true });
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
});
