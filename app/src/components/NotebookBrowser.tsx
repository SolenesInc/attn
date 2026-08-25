import type { FsEntry, FsExistsResult, FsReadAssetResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult } from '../hooks/useDaemonSocket';
import { NotebookSurface } from './NotebookSurface';

interface NotebookBrowserProps {
  isOpen: boolean;
  initialPath?: string | null;
  onClose: () => void;
  listDir: (path: string) => Promise<FsEntry[]>;
  readFile: (path: string) => Promise<FsReadResult>;
  // Omit baseHash to create-only; pass the loaded hash to edit (hash-CAS).
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  existsFile: (path: string) => Promise<FsExistsResult>;
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  changeSignal?: number;
  listFiles: () => Promise<NotebookEntry[]>;
  // true = a chief session is working, false = idle, undefined = no chief session at all.
  chiefActive?: boolean;
}

export function NotebookBrowser({ isOpen, onClose, ...rest }: NotebookBrowserProps) {
  return <NotebookSurface variant="modal" active={isOpen} onClose={onClose} {...rest} />;
}
