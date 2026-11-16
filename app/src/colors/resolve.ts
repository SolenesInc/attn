import {
  type AppearanceColors,
  type AppearanceName,
  findScheme,
} from './schemes';

export type ThemePreference = 'dark' | 'light' | 'system';

export const THEME_SETTING = 'theme';
export const COLOR_SCHEME_SETTING = 'color_scheme';
export const COLOR_OVERRIDES_SETTING = 'color_overrides';

type Overrides = Partial<Record<AppearanceName, Partial<{ [G in keyof AppearanceColors]: Partial<AppearanceColors[G]> }>>>;

export interface Colors extends AppearanceColors {
  appearance: AppearanceName;
}

export function themePreference(settings: Record<string, string>): ThemePreference {
  const value = settings[THEME_SETTING];
  return value === 'light' || value === 'system' ? value : 'dark';
}

export function systemAppearance(): AppearanceName {
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

export function hasColorOverrides(settings: Record<string, string>): boolean {
  const overrides = parseOverrides(settings[COLOR_OVERRIDES_SETTING]);
  return Object.values(overrides).some((groups) => Object.values(groups ?? {}).some((names) => Object.keys(names ?? {}).length > 0));
}

function parseOverrides(value: string | undefined): Overrides {
  return (value ? JSON.parse(value) as Overrides | null : null) ?? {};
}

export function resolveColors(
  scheme: string | undefined,
  overrides: string | undefined,
  appearance: AppearanceName,
): Colors {
  const base = findScheme(scheme)[appearance];
  const over = parseOverrides(overrides)[appearance] ?? {};
  return {
    appearance,
    ui: { ...base.ui, ...over.ui },
    terminal: { ...base.terminal, ...over.terminal },
  };
}

export function applyColors(colors: Colors, root: HTMLElement = document.documentElement): void {
  root.setAttribute('data-theme', colors.appearance);
  root.style.colorScheme = colors.appearance;
  for (const [name, value] of Object.entries(colors.ui)) {
    root.style.setProperty(`--color-${name.replace(/_/g, '-')}`, value);
  }
}
