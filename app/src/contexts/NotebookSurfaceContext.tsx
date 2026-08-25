import { createContext, useContext, type ReactNode } from 'react';
import type {
  FsEntry,
  FsExistsResult,
  FsReadAssetResult,
  FsReadResult,
  FsWatchResult,
  FsWriteResult,
  NotebookEntry,
  NotebookSendToChiefResult,
} from '../hooks/useDaemonSocket';

export interface NotebookSurfaceDaemon {
  listDir: (path: string) => Promise<FsEntry[]>;
  readFile: (path: string) => Promise<FsReadResult>;
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  existsFile: (path: string) => Promise<FsExistsResult>;
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Flat list for a tile's ⌘P finder. Unlike backlinksNotebook/sendToChief below,
  // this one DOES follow `root`.
  listFiles: () => Promise<NotebookEntry[]>;
  changeSignal: number;
}

// CRITICAL BOUNDARY: backlinksNotebook and sendToChief stay bound to the notebook root
// whatever `root` says — widening them to an arbitrary filesystem root is forbidden.
export type MakeNotebookSurfaceDaemon = (root?: string) => NotebookSurfaceDaemon;

export interface NotebookSurfaceContextValue {
  makeDaemon: MakeNotebookSurfaceDaemon;
  effectiveNotebookRoot: string;
  sendFsWatch: (root?: string) => Promise<FsWatchResult>;
  sendFsUnwatch: (root?: string) => Promise<FsWatchResult>;
  // Bumps on every fresh connect. The daemon drops fs_watch refs on disconnect,
  // so a root-bound tile must re-issue fs_watch per generation or go blind.
  connectionGeneration: number;
}

const NotebookSurfaceContext = createContext<NotebookSurfaceContextValue | null>(null);

export function NotebookSurfaceProvider({ value, children }: { value: NotebookSurfaceContextValue; children: ReactNode }) {
  return <NotebookSurfaceContext.Provider value={value}>{children}</NotebookSurfaceContext.Provider>;
}

export function useNotebookSurfaceContext(): NotebookSurfaceContextValue {
  const ctx = useContext(NotebookSurfaceContext);
  if (!ctx) {
    throw new Error('useNotebookSurfaceContext must be used within a NotebookSurfaceProvider');
  }
  return ctx;
}
