import { type CSSProperties, useCallback, useEffect, useRef, useState } from 'react';
import type { FsEntry } from '../../hooks/useDaemonSocket';
import './FileTree.css';

interface FileTreeProps {
  listDir: (path: string) => Promise<FsEntry[]>;
  selectedPath: string | null;
  onSelectFile: (path: string) => void;
  changeSignal?: number;
}

const ROOT = '';

export function FileTree({ listDir, selectedPath, onSelectFile, changeSignal = 0 }: FileTreeProps) {
  const [children, setChildren] = useState<Map<string, FsEntry[]>>(new Map());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Map<string, string>>(new Map());

  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const markLoading = useCallback((dir: string, on: boolean) => {
    setLoading((prev) => {
      const next = new Set(prev);
      if (on) next.add(dir);
      else next.delete(dir);
      return next;
    });
  }, []);

  const loadDir = useCallback(
    async (dir: string) => {
      markLoading(dir, true);
      try {
        const entries = await listDir(dir);
        if (!mountedRef.current) return;
        setChildren((prev) => new Map(prev).set(dir, entries));
        setErrors((prev) => {
          if (!prev.has(dir)) return prev;
          const next = new Map(prev);
          next.delete(dir);
          return next;
        });
      } catch (err) {
        if (!mountedRef.current) return;
        setErrors((prev) =>
          new Map(prev).set(dir, err instanceof Error ? err.message : 'Could not list this folder'),
        );
      } finally {
        if (mountedRef.current) markLoading(dir, false);
      }
    },
    [listDir, markLoading],
  );

  useEffect(() => {
    void loadDir(ROOT);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadDir]);

  useEffect(() => {
    if (changeSignal === 0) return;
    void loadDir(ROOT);
    for (const dir of expanded) void loadDir(dir);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeSignal, loadDir]);

  // The load decision is made outside any setState updater — StrictMode invokes
  // updaters twice.
  const toggleDir = useCallback(
    (dir: string) => {
      const isOpen = expanded.has(dir);
      setExpanded((prev) => {
        const next = new Set(prev);
        if (next.has(dir)) next.delete(dir);
        else next.add(dir);
        return next;
      });
      if (!isOpen && !children.has(dir)) void loadDir(dir);
    },
    [expanded, children, loadDir],
  );

  const renderDir = (dir: string, depth: number) => {
    const entries = children.get(dir);
    const isLoading = loading.has(dir);
    const error = errors.get(dir);

    if (error) {
      return (
        <li className="file-tree-state file-tree-error" style={indent(depth)} role="treeitem">
          {error}
        </li>
      );
    }
    if (entries === undefined) {
      return isLoading ? (
        <li className="file-tree-state" style={indent(depth)} role="treeitem">
          Loading…
        </li>
      ) : null;
    }
    if (entries.length === 0) {
      return (
        <li className="file-tree-state file-tree-empty" style={indent(depth)} role="treeitem">
          Empty
        </li>
      );
    }
    return entries.map((entry) =>
      entry.isDir ? (
        <li key={entry.path} role="none">
          <button
            type="button"
            role="treeitem"
            aria-expanded={expanded.has(entry.path)}
            className="file-tree-row file-tree-dir"
            style={indent(depth)}
            onClick={() => toggleDir(entry.path)}
            title={entry.path}
          >
            <span className={`file-tree-chevron${expanded.has(entry.path) ? ' is-open' : ''}`} aria-hidden="true">
              ▸
            </span>
            <span className="file-tree-name">{entry.name}</span>
          </button>
          {expanded.has(entry.path) && (
            <ul role="group" className="file-tree-group">
              {renderDir(entry.path, depth + 1)}
            </ul>
          )}
        </li>
      ) : (
        <li key={entry.path} role="none">
          <button
            type="button"
            role="treeitem"
            aria-current={entry.path === selectedPath ? 'true' : undefined}
            className={`file-tree-row file-tree-file${entry.path === selectedPath ? ' is-selected' : ''}`}
            style={indent(depth)}
            onClick={() => onSelectFile(entry.path)}
            title={entry.path}
          >
            <span className="file-tree-name">{entry.name}</span>
          </button>
        </li>
      ),
    );
  };

  const rootEntries = children.get(ROOT);
  const rootLoading = loading.has(ROOT);
  const rootError = errors.get(ROOT);

  return (
    <ul className="file-tree" role="tree" aria-label="Files">
      {rootError ? (
        <li className="file-tree-state file-tree-error" role="treeitem">
          {rootError}
        </li>
      ) : rootEntries === undefined ? (
        rootLoading ? (
          <li className="file-tree-state" role="treeitem">
            Loading…
          </li>
        ) : null
      ) : rootEntries.length === 0 ? (
        <li className="file-tree-state file-tree-empty" role="treeitem">
          This folder is empty.
        </li>
      ) : (
        renderDir(ROOT, 0)
      )}
    </ul>
  );
}

function indent(depth: number): CSSProperties {
  return { paddingLeft: `${8 + depth * 14}px` };
}
