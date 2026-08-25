import type { FsIndexResult, NotebookEntry } from '../hooks/useDaemonSocket';

export function fsChangeSignalKey(root: string, effectiveNotebookRoot: string): string {
  return root || effectiveNotebookRoot;
}

export function bumpFsChangeSignal(
  signals: Record<string, number>,
  root: string,
  effectiveNotebookRoot: string,
): Record<string, number> {
  const key = fsChangeSignalKey(root, effectiveNotebookRoot);
  return { ...signals, [key]: (signals[key] || 0) + 1 };
}

// size is always 0: fs_index omits stat() per entry to stay fast over large repos,
// and nothing downstream reads it.
export function fsIndexToNotebookEntries(result: FsIndexResult): NotebookEntry[] {
  if (result.truncated) {
    console.warn('[App] fs_index truncated for', result.root, '— finder list is incomplete');
  }
  return result.files.map((path) => ({ path, size: 0 }));
}
