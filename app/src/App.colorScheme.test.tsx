import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import attn from '../../internal/colors/schemes/attn.json';
import catppuccin from '../../internal/colors/schemes/catppuccin.json';
import frappe from '../../internal/colors/schemes/catppuccin-frappe.json';
import { gesture, renderApp } from './test/renderApp';
import { openSection, savedSettings } from './test/settings';

type Terminal = typeof attn.dark.terminal;

const rootColor = (name: string) => document.documentElement.style.getPropertyValue(`--color-${name}`);
const ANSI = [
  'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
  'bright_black', 'bright_red', 'bright_green', 'bright_yellow', 'bright_blue', 'bright_magenta', 'bright_cyan', 'bright_white',
] as const;
const terminalTheme = (terminal: Terminal) => ({
  cmd: 'set_terminal_theme',
  foreground: terminal.fg,
  background: terminal.bg,
  cursor: terminal.cursor,
  ansi_palette: ANSI.map((name) => terminal[name]),
});

describe('App color scheme', () => {
  it('applies a color_scheme from the daemon to the root and the terminal theme', async () => {
    const { daemon } = await renderApp();
    expect(rootColor('bg-app')).toBe(attn.dark.ui.bg_app);

    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { color_scheme: 'catppuccin' } }));

    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    expect(rootColor('bg-app')).toBe(catppuccin.dark.ui.bg_app);
    expect(rootColor('state-working')).toBe(catppuccin.dark.ui.state_working);
    expect(rootColor('bg-overlay')).toBe(catppuccin.dark.ui.bg_overlay);
    expect(daemon.sentOf('set_terminal_theme')).toEqual([terminalTheme(attn.dark.terminal), terminalTheme(catppuccin.dark.terminal)]);
  });

  it('lays color_overrides over the scheme for the appearance in effect', async () => {
    const overrides = { dark: { ui: { bg_app: '#24273a' }, terminal: { bg: '#24273a', bright_black: '#7f849c' } }, light: { ui: { bg_app: '#ffeedd' } } };
    const { daemon } = await renderApp({
      initialState: { settings: { color_scheme: 'catppuccin', color_overrides: JSON.stringify(overrides) } },
    });

    expect(rootColor('bg-app')).toBe('#24273a');
    expect(rootColor('bg-panel')).toBe(catppuccin.dark.ui.bg_panel);
    expect(daemon.sentOf('set_terminal_theme')).toEqual([
      terminalTheme({ ...catppuccin.dark.terminal, bg: '#24273a', bright_black: '#7f849c' }),
    ]);
  });

  it('follows a theme change from the daemon without a reload', async () => {
    const { daemon } = await renderApp({ initialState: { settings: { color_scheme: 'catppuccin-frappe' } } });
    expect(rootColor('bg-app')).toBe(frappe.dark.ui.bg_app);

    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { color_scheme: 'catppuccin-frappe', theme: 'light' } }));

    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    expect(rootColor('bg-app')).toBe(frappe.light.ui.bg_app);
    expect(daemon.sentOf('set_terminal_theme')).toEqual([terminalTheme(frappe.dark.terminal), terminalTheme(frappe.light.terminal)]);
  });

  it('saves the scheme picked in Settings and applies it', async () => {
    const daemon = await openSection('general');
    expect(screen.getByRole('button', { name: 'attn' })).toHaveAttribute('aria-pressed', 'true');

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Catppuccin Frappé' })));

    expect(savedSettings(daemon)).toEqual([['color_scheme', 'catppuccin-frappe']]);
    expect(screen.getByRole('button', { name: 'Catppuccin Frappé' })).toHaveAttribute('aria-pressed', 'true');
    expect(rootColor('bg-app')).toBe(frappe.dark.ui.bg_app);
  });

  it('marks custom colors and clears them with Reset', async () => {
    const daemon = await openSection('general', { settings: { color_overrides: '{"dark":{"ui":{"bg_app":"#24273a"}}}' } });
    expect(screen.getByText('Custom colors')).toBeInTheDocument();
    expect(rootColor('bg-app')).toBe('#24273a');

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reset' })));

    expect(savedSettings(daemon)).toEqual([['color_overrides', '']]);
    expect(screen.queryByText('Custom colors')).toBeNull();
    expect(rootColor('bg-app')).toBe(attn.dark.ui.bg_app);
  });
});
