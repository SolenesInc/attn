/** The name attn itself moves seeds under. Mirrors `internal/crew.DaemonID`. */
export const CREW_DAEMON_ID = 'attn';

/** Display capitalizes the first character of a crew id; identity stays lowercase in
 * paths, CLI args, protocol fields. Mirrors `internal/crew.DisplayName`. */
export function crewDisplayName(id: string): string {
  const trimmed = id.trim();
  if (!trimmed) return '';
  if (trimmed === CREW_DAEMON_ID) return trimmed;
  const [first] = trimmed;
  return first.toUpperCase() + trimmed.slice(first.length);
}

/** Addresses a member by its permanent key; a bare word would be read as a name. */
export function crewRef(key: string): string {
  return `member:${key}`;
}
