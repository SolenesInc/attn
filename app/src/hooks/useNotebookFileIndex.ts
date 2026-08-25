import { useEffect, useRef, useState } from 'react';
import type { NotebookEntry } from './useDaemonSocket';

export const FILE_INDEX_REFETCH_DEBOUNCE_MS = 300;

export interface NotebookFileIndex {
  files: NotebookEntry[];
  loading: boolean;
  error: string | null;
}

export function useNotebookFileIndex(
  listFiles: (() => Promise<NotebookEntry[]>) | undefined,
  changeSignal: number,
  enabled: boolean,
): NotebookFileIndex {
  const [files, setFiles] = useState<NotebookEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const seqRef = useRef(0);
  const didInitialRef = useRef(false);

  useEffect(() => {
    if (!enabled || !listFiles) {
      seqRef.current += 1;
      didInitialRef.current = false;
      setFiles([]);
      setError(null);
      setLoading(false);
      return;
    }
    const delay = didInitialRef.current ? FILE_INDEX_REFETCH_DEBOUNCE_MS : 0;
    didInitialRef.current = true;
    const seq = ++seqRef.current;
    setLoading(true);
    const timer = window.setTimeout(() => {
      void listFiles()
        .then((result) => {
          if (seqRef.current !== seq) return;
          setFiles(result);
          setError(null);
          setLoading(false);
        })
        .catch((err) => {
          if (seqRef.current !== seq) return;
          setError(err instanceof Error ? err.message : 'Could not list notebook files');
          setLoading(false);
        });
    }, delay);
    return () => window.clearTimeout(timer);
  }, [enabled, listFiles, changeSignal]);

  return { files, loading, error };
}
