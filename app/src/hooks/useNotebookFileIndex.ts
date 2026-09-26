import { useEffect, useRef, useState } from 'react';
import type { NotebookEntry } from './useDaemonSocket';

const FILE_INDEX_REFETCH_DEBOUNCE_MS = 300;

interface NotebookFileIndex {
  files: NotebookEntry[];
  loading: boolean;
}

export function useNotebookFileIndex(
  listFiles: (() => Promise<NotebookEntry[]>) | undefined,
  changeSignal: number,
  enabled: boolean,
): NotebookFileIndex {
  const [files, setFiles] = useState<NotebookEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const seqRef = useRef(0);
  const didInitialRef = useRef(false);

  useEffect(() => {
    if (!enabled || !listFiles) {
      seqRef.current += 1;
      didInitialRef.current = false;
      setFiles([]);
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
          setLoading(false);
        })
        .catch(() => {
          if (seqRef.current !== seq) return;
          setLoading(false);
        });
    }, delay);
    return () => window.clearTimeout(timer);
  }, [enabled, listFiles, changeSignal]);

  return { files, loading };
}
