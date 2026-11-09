import type { Seed } from '../types/generated';
import { SEED_ID_BODY } from './seedId';

export type SeedMentionPiece = string | { seedId: string; text: string };

export interface SeedMentionIndex {
  split: (text: string) => SeedMentionPiece[];
}

const NAME_CHAR = '[A-Za-z0-9_-]';
const NAME_CHAR_NOT = '[^A-Za-z0-9_-]';

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Resolves ids and unambiguous step slugs of the given seeds; null when there are none. */
export function createSeedMentionIndex(seeds: readonly Pick<Seed, 'id' | 'step_slug'>[]): SeedMentionIndex | null {
  if (seeds.length === 0) return null;
  const ids = new Set(seeds.map((seed) => seed.id));
  const slugOwners = new Map<string, string[]>();
  for (const seed of seeds) {
    if (!seed.step_slug) continue;
    slugOwners.set(seed.step_slug, [...(slugOwners.get(seed.step_slug) ?? []), seed.id]);
  }
  const seedIdBySlug = new Map<string, string>();
  for (const [slug, owners] of slugOwners) {
    if (owners.length === 1 && !ids.has(slug)) seedIdBySlug.set(slug, owners[0]);
  }
  const slugs = [...seedIdBySlug.keys()].sort((a, b) => b.length - a.length).map(escapeRegExp);
  const alternatives = [`s-${SEED_ID_BODY}`, ...slugs].join('|');
  const pattern = new RegExp(`(^|${NAME_CHAR_NOT})(${alternatives})(?!${NAME_CHAR})`, 'g');
  return {
    split(text) {
      const pieces: SeedMentionPiece[] = [];
      let last = 0;
      for (const match of text.matchAll(pattern)) {
        const name = match[2];
        const seedId = ids.has(name) ? name : seedIdBySlug.get(name);
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
