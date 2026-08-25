/** GitHub-style heading slugs, ported from plannotator's `slugifyHeading` —
 * unicode-preserving, unlike a naive `[^\w]` strip. */
export function slugifyHeading(text: string): string {
  return text
    .toLowerCase()
    .replace(/\[\[([^\]]+)\]\]/g, '$1') // [[wiki]] → wiki
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1') // [label](url) → label
    .replace(/[*_`~]/g, '') // strip emphasis/code markers
    .replace(/[^\p{L}\p{N}]+/gu, '-') // non letter/number runs → '-'
    .replace(/^-+|-+$/g, ''); // trim hyphens
}

/** Per-document dedup: the first occurrence keeps the bare slug, later ones get
 * `-1`, `-2`, …; an empty slug yields undefined. One slugger per document render. */
export function createSlugger(): (text: string) => string | undefined {
  const counts = new Map<string, number>();
  return (text: string) => {
    const base = slugifyHeading(text);
    if (!base) {
      return undefined;
    }
    const count = counts.get(base) ?? 0;
    counts.set(base, count + 1);
    return count === 0 ? base : `${base}-${count}`;
  };
}
