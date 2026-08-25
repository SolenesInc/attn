// The opener's index-and-open route runs only against the local daemon, so a remote
// session's `cwd` must be rejected: an existing local path would show the wrong files.
export interface OpenerSessionLike {
  id: string;
  cwd?: string;
  endpointId?: string;
}

export interface MarkdownOpenerTarget {
  /** Root for fuzzy indexing, or null when there is no local project context. */
  root: string | null;
  /** Session to bind an opened file to. `''` means the daemon's selected session;
   * `null` means no local session owns this open, so it must not reach the daemon. */
  sessionId: string | null;
}

export function resolveMarkdownOpenerTarget(
  session: OpenerSessionLike | undefined,
  notebookRoot: string | undefined | null,
): MarkdownOpenerTarget {
  const fallbackRoot = notebookRoot || null;
  if (!session) return { root: fallbackRoot, sessionId: '' };
  if (session.endpointId) return { root: fallbackRoot, sessionId: null };
  return { root: session.cwd || fallbackRoot, sessionId: session.id };
}
