import type { AskSeedMention } from '../types/generated';

export type SeedMentionPiece = string | { seedId: string; text: string };

export interface SeedMentionIndex {
  split: (text: string) => SeedMentionPiece[];
}

const NAME_CHAR = '[A-Za-z0-9_-]';
const NAME_CHAR_NOT = '[^A-Za-z0-9_-]';

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function createSeedMentionIndex(mentions: readonly AskSeedMention[]): SeedMentionIndex | null {
  if (mentions.length === 0) return null;
  const seedIdByName = new Map(mentions.map(({ text, seed_id }) => [text, seed_id]));
  const alternatives = [...seedIdByName.keys()].sort((a, b) => b.length - a.length).map(escapeRegExp).join('|');
  const pattern = new RegExp(`(^|${NAME_CHAR_NOT})(${alternatives})(?!${NAME_CHAR})`, 'g');
  return {
    split(text) {
      const pieces: SeedMentionPiece[] = [];
      let last = 0;
      for (const match of text.matchAll(pattern)) {
        const name = match[2];
        const seedId = seedIdByName.get(name);
        if (!seedId) continue;
        const start = (match.index ?? 0) + match[1].length;
        if (start > last) pieces.push(text.slice(last, start));
        pieces.push({ seedId, text: name });
        last = start + name.length;
      }
      if (last === 0) return [text];
      if (last < text.length) pieces.push(text.slice(last));
      return pieces;
    },
  };
}
