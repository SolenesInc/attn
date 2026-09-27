import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished } from 'vitest';
import { stubNavigatorPlatform } from './test/platformStub';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

async function openCheatsheet(platform?: string) {
  if (platform) onTestFinished(stubNavigatorPlatform(platform));
  const view = await renderApp();
  await gesture(view.daemon, () => pressShortcut('ui.showShortcuts'));
  return { ...view, cheatsheet: screen.getByRole('dialog', { name: 'Keyboard Shortcuts' }) };
}

function rows(cheatsheet: HTMLElement) {
  return Array.from(cheatsheet.querySelectorAll('.shortcuts-row'), (row) => ({
    label: row.querySelector('.shortcuts-row-label')?.firstChild?.textContent ?? '',
    combos: Array.from(row.querySelectorAll('.key-combo'), (combo) =>
      Array.from(combo.querySelectorAll('.keycap'), (cap) => cap.textContent ?? '')),
  }));
}

function combosOf(cheatsheet: HTMLElement, label: string) {
  return rows(cheatsheet).find((row) => row.label === label)?.combos;
}

describe('App shortcut cheatsheet', () => {
  it('lists every action with keys under its category', async () => {
    const { cheatsheet } = await openCheatsheet();

    expect(within(cheatsheet).getByRole('heading', { name: 'Desktops & Sessions' })).toBeInTheDocument();
    expect(within(cheatsheet).getByRole('heading', { name: 'Panes & Terminals' })).toBeInTheDocument();
    const listed = rows(cheatsheet);
    expect(listed.length).toBeGreaterThan(20);
    expect(listed.filter(({ label, combos }) => !label || combos.length === 0 || combos.some((combo) => combo.length === 0 || combo.includes('')))).toEqual([]);
  });

  it.each([
    ['macOS', 'MacIntel', {
      'Desktop overview': [['⌘', 'G']],
      'New session on this desktop': [['⌘', 'N']],
      'Switch to desktop 1–9': [['⌘', '1–9']],
      'Previous / next desktop': [['⌘', '↑'], ['⌘', '↓']],
      'Back / forward through agent history': [['⌘', '['], ['⌘', ']']],
    }],
    ['Linux', 'Linux x86_64', {
      'Desktop overview': [['Ctrl', 'Shift', 'G']],
      'Switch to desktop 1–9': [['Ctrl', 'Shift', '1–9']],
      'Agent palette': [['Ctrl', 'Shift', 'K']],
      'Move focus between panes': [['Ctrl', 'Shift', '←↑→↓']],
      'Back / forward through agent history': [['Ctrl', 'Shift', '{'], ['Ctrl', 'Shift', '}']],
    }],
  ])('shows the %s keys', async (_, platform, expected) => {
    const { cheatsheet } = await openCheatsheet(platform);

    for (const [label, combos] of Object.entries(expected)) {
      expect(combosOf(cheatsheet, label), label).toEqual(combos);
    }
    if (platform !== 'MacIntel') expect(cheatsheet).not.toHaveTextContent('⌘');
  });

  it('closes from its close button', async () => {
    const { daemon } = await openCheatsheet();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close keyboard shortcuts' })));

    expect(screen.queryByRole('dialog', { name: 'Keyboard Shortcuts' })).toBeNull();
  });
});
