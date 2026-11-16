import attn from '../../../internal/colors/schemes/attn.json';

export type Scheme = typeof attn;
export type AppearanceName = 'dark' | 'light';
export type AppearanceColors = Scheme['dark'];
export type UiColors = AppearanceColors['ui'];
export type TerminalColors = AppearanceColors['terminal'];

export interface SchemeEntry {
  id: string;
  scheme: Scheme;
}

const files = import.meta.glob<Scheme>('../../../internal/colors/schemes/*.json', { eager: true, import: 'default' });

export const DEFAULT_SCHEME_ID = 'attn';

export const SCHEMES: SchemeEntry[] = Object.entries(files)
  .map(([path, scheme]) => ({ id: path.slice(path.lastIndexOf('/') + 1, -'.json'.length), scheme }))
  .sort((a, b) => (a.id === DEFAULT_SCHEME_ID ? -1 : b.id === DEFAULT_SCHEME_ID ? 1 : a.id.localeCompare(b.id)));

export function findScheme(id: string | undefined): Scheme {
  return SCHEMES.find((entry) => entry.id === id)?.scheme ?? attn;
}

export const TERMINAL_ANSI_NAMES = [
  'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
  'bright_black', 'bright_red', 'bright_green', 'bright_yellow',
  'bright_blue', 'bright_magenta', 'bright_cyan', 'bright_white',
] as const satisfies readonly (keyof TerminalColors)[];

export function terminalAnsiPalette(terminal: TerminalColors): string[] {
  return TERMINAL_ANSI_NAMES.map((name) => terminal[name]);
}
