export const COMMAND_PREFIX = '>';

export type PaletteMode = 'agents' | 'commands';

export type PaletteState =
  | { mode: 'search'; query: string }
  | { mode: 'snooze'; sessionId: string; openedAt: Date; query: string };

export function openPalette(mode: PaletteMode): PaletteState {
  return { mode: 'search', query: mode === 'commands' ? COMMAND_PREFIX : '' };
}

export function switchPalette(state: PaletteState, mode: PaletteMode): PaletteState | null {
  const showingCommands = state.query.startsWith(COMMAND_PREFIX);
  if (state.mode === 'search' && showingCommands === (mode === 'commands')) return null;
  const text = showingCommands ? state.query.slice(COMMAND_PREFIX.length) : state.query;
  return { mode: 'search', query: mode === 'commands' ? `${COMMAND_PREFIX}${text}` : text };
}
