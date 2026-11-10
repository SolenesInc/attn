import { useEffect, useMemo, useState } from 'react';
import { Palette } from './Palette';
import { finderBasename } from './rank';
import { mergeOpenerFiles, rankOpenerFiles, type OpenerRecent } from './openerRank';
import { useSessionStore } from '../../store/sessions';
import { isPathQuery, toBrowseInput, descendQuery } from './pathMode';
import { useFilesystemSuggestions } from '../../hooks/useFilesystemSuggestions';
import type { BrowseDirectoryResult } from '../../hooks/useDaemonSocket';

export const OPENER_EXTENSIONS = ['md'];

export interface MarkdownOpenerProps {
  root: string | null;
  loadRecents: () => Promise<OpenerRecent[]>;
  loadIndex: (root: string) => Promise<{ files: string[]; truncated: boolean }>;
  browseDirectory?: (inputPath: string, extensions?: string[]) => Promise<BrowseDirectoryResult>;
  onPick: (absPath: string, host?: string) => void;
  onClose: () => void;
}

interface OpenerRow {
  key: string;
  title: string;
  path: string;
  isDir: boolean;
  target: string;
  host?: string;
}

export function MarkdownOpener({ root, loadRecents, loadIndex, browseDirectory, onPick, onClose }: MarkdownOpenerProps) {
  const [query, setQuery] = useState('');
  const [recents, setRecents] = useState<OpenerRecent[]>([]);
  const outposts = useSessionStore((state) => state.outposts);
  const [indexFiles, setIndexFiles] = useState<string[]>([]);
  const [truncated, setTruncated] = useState(false);
  const [indexLoading, setIndexLoading] = useState(!!root);

  useEffect(() => {
    let cancelled = false;
    void loadRecents()
      .then((files) => { if (!cancelled) setRecents(files); })
      .catch((error) => { console.error('[MarkdownOpener] recent files failed:', error); });
    return () => { cancelled = true; };
  }, [loadRecents]);

  useEffect(() => {
    if (!root) {
      setIndexFiles([]);
      setIndexLoading(false);
      return;
    }
    let cancelled = false;
    setIndexLoading(true);
    void loadIndex(root)
      .then((result) => {
        if (cancelled) return;
        setIndexFiles(result.files);
        setTruncated(result.truncated);
      })
      .catch((error) => {
        if (!cancelled) console.error('[MarkdownOpener] file index failed:', error);
      })
      .finally(() => { if (!cancelled) setIndexLoading(false); });
    return () => { cancelled = true; };
  }, [root, loadIndex]);

  const browseInput = toBrowseInput(query, root);
  const pathMode = isPathQuery(query);
  const { suggestions, loading: browseLoading, error: browseError } = useFilesystemSuggestions(
    browseInput || '',
    browseDirectory,
    { enabled: pathMode && !!browseInput, extensions: OPENER_EXTENSIONS },
  );

  const candidates = useMemo(
    () => mergeOpenerFiles(recents, root, indexFiles),
    [recents, root, indexFiles],
  );
  const fuzzyRows = useMemo<OpenerRow[]>(
    () => rankOpenerFiles(candidates, query).map((file) => ({
      key: file.host ? `${file.host}:${file.absPath}` : file.absPath,
      title: finderBasename(file.label),
      path: file.host ? `${file.label} on ${outposts.find((row) => row.id === file.host)?.name ?? file.host}` : file.label,
      isDir: false,
      target: file.absPath,
      ...(file.host && { host: file.host }),
    })),
    [candidates, query, outposts],
  );
  const pathRows = useMemo<OpenerRow[]>(
    () => suggestions.map((entry) => ({
      key: entry.absPath,
      title: entry.isDir ? `${entry.name}/` : entry.name,
      path: entry.path,
      isDir: entry.isDir,
      target: entry.isDir ? descendQuery(entry.path) : entry.absPath,
    })),
    [suggestions],
  );
  const rows = pathMode ? pathRows : fuzzyRows;

  const emptyLabel = pathMode
    ? pathModeEmptyLabel(browseInput, browseLoading, browseError, !!browseDirectory)
    : indexLoading
      ? 'Loading files…'
      : query.trim() === ''
        ? 'No recently opened files. Type to search, or a path to browse.'
        : truncated
          ? 'No files match (the index is capped, so some files are missing). Type a path to browse.'
          : 'No files match. Type a path to browse.';

  return (
    <Palette<OpenerRow>
      variant="markdown-opener"
      ariaLabel="Open a markdown file"
      placeholder="Open a markdown file…"
      query={query}
      onQueryChange={setQuery}
      items={rows}
      itemKey={(row) => row.key}
      emptyLabel={emptyLabel}
      onPick={(row) => {
        if (row.isDir) setQuery(row.target);
        else onPick(row.target, row.host);
      }}
      onClose={onClose}
      renderItem={(row) => (
        <>
          <span className="palette-option-title markdown-opener-option-title">{row.title}</span>
          <span className="palette-option-path markdown-opener-option-path">{row.path}</span>
        </>
      )}
    />
  );
}

function pathModeEmptyLabel(
  browseInput: string | null,
  loading: boolean,
  error: string | null,
  hasBrowse: boolean,
): string {
  if (!hasBrowse) return 'Browsing paths is unavailable.';
  if (!browseInput) return 'No folder to resolve this path against.';
  if (loading) return 'Listing…';
  if (error) return 'That folder could not be listed.';
  return 'Nothing here matches.';
}
