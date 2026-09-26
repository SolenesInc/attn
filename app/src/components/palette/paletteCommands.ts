import type { ReactNode } from 'react';

export interface PaletteCommand {
  id: string;
  title: string;
  description?: string;
  keywords?: string[];
  icon?: ReactNode;
  shortcut?: string[][];
  detail?: string;
  run: () => void;
}

function commandScore(command: PaletteCommand, terms: readonly string[]): number {
  if (terms.length === 0) return 1;
  const title = command.title.toLowerCase();
  const searchable = [command.title, command.description ?? '', ...(command.keywords ?? [])].join(' ').toLowerCase();
  if (!terms.every((term) => searchable.includes(term))) return 0;
  return terms.reduce((score, term) => {
    if (title.startsWith(term)) return score + 4;
    if (title.includes(term)) return score + 2;
    return score + 1;
  }, 0);
}

export function filterCommands(commands: readonly PaletteCommand[], query: string): PaletteCommand[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  return commands
    .map((command) => ({ command, score: commandScore(command, terms) }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => right.score - left.score)
    .map(({ command }) => command);
}
