export const SEED_ID_BODY = '[0-9a-hjkmnp-tv-z]{6}';

const SEED_ID_EXACT = new RegExp(`^s-${SEED_ID_BODY}$`);

export function isSeedId(value: string): boolean {
  return SEED_ID_EXACT.test(value);
}
