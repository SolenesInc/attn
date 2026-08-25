/** Display capitalizes the first character of a crew id; identity stays lowercase in
 * paths, CLI args, protocol fields. Mirrors `internal/crew.DisplayName`. */
export function crewDisplayName(id: string): string {
  const trimmed = id.trim();
  if (!trimmed) return '';
  const [first] = trimmed;
  return first.toUpperCase() + trimmed.slice(first.length);
}

/** A holder is a member's display name, or a bare session id left as it is.
 * Mirrors `internal/crew.HolderName`. */
export function crewHolderName(member: string | undefined, session: string | undefined): string {
  return member?.trim() ? crewDisplayName(member) : session?.trim() ?? '';
}
