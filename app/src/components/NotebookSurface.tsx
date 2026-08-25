import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { FsEntry, FsExistsResult, FsReadAssetResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useNotebookFileIndex } from '../hooks/useNotebookFileIndex';
import { useTileAutoFold } from '../hooks/useTileAutoFold';
import { notebookLinkPath } from './notebook/brokenLinks';
import { FileTree } from './notebook/FileTree';
import { fileKind, isBinaryPath, isMarkdownPath } from './notebook/fileKind';
import { parseFrontmatter } from './notebook/frontmatter';
import { headingSlug, noteDir, resolveNotebookLink } from './notebook/linkResolver';
import { LiveMarkdownEditor, type LiveMarkdownEditorHandle, type LiveSelection } from './notebook/LiveMarkdownEditor';
import { NotebookFinder } from './notebook/NotebookFinder';
import { registerPaletteClaim } from './palette/paletteClaim';
import { parseOutline } from './notebook/outline';
import './NotebookBrowser.css';

export interface NotebookSurfaceProps {
  variant: 'modal' | 'tile';
  active: boolean;
  initialPath?: string | null;
  onClose?: () => void;
  onOpenFile?: (path: string) => void;
  listDir: (path: string) => Promise<FsEntry[]>;
  readFile: (path: string) => Promise<FsReadResult>;
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  existsFile: (path: string) => Promise<FsExistsResult>;
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  backlinksNotebook?: (path: string) => Promise<NotebookEntry[]>;
  sendToChief?: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  changeSignal?: number;
  listFiles?: () => Promise<NotebookEntry[]>;
  chiefActive?: boolean;
}

const PREFERRED_FIRST = ['knowledge/index.md', 'index.md'];

const AUTOSAVE_DELAY_MS = 700;

// Callers MUST react to 'conflict'/'error': dropping one loses the user's edits behind a
// navigation with no banner shown.
export type PersistOutcome = 'saved' | 'conflict' | 'error' | 'noop';

export interface NotebookSurfaceHandle {
  flushPendingSave: () => Promise<PersistOutcome>;
}

export const NotebookSurface = forwardRef<NotebookSurfaceHandle, NotebookSurfaceProps>(function NotebookSurface({
  variant,
  active,
  initialPath,
  onClose,
  onOpenFile,
  listDir,
  readFile,
  writeFile,
  existsFile,
  readAsset,
  backlinksNotebook,
  sendToChief,
  changeSignal = 0,
  listFiles,
  chiefActive,
}: NotebookSurfaceProps, ref) {
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [note, setNote] = useState<FsReadResult | null>(null);
  const [noteError, setNoteError] = useState<string | null>(null);
  const [noteLoading, setNoteLoading] = useState(false);
  const [backlinks, setBacklinks] = useState<NotebookEntry[]>([]);
  const [backlinksLoading, setBacklinksLoading] = useState(false);
  const selectedPathRef = useRef<string | null>(null);
  selectedPathRef.current = selectedPath;
  const loadSeqRef = useRef(0);
  const persistRef = useRef<() => Promise<PersistOutcome>>(async () => 'noop');
  const dialogRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<LiveMarkdownEditorHandle>(null);
  const [outlineOpen, setOutlineOpen] = useState(true);
  const [backlinksOpen, setBacklinksOpen] = useState(true);
  const [searchOpen, setSearchOpen] = useState(false);
  const [treeOverride, setTreeOverride] = useState<boolean | null>(null);
  const [railOverride, setRailOverride] = useState<boolean | null>(null);
  const { treeAutoFold, railAutoFold } = useTileAutoFold(bodyRef, variant === 'tile');
  const treeFolded = treeOverride === null ? treeAutoFold : treeOverride;
  const railFolded = railOverride === null ? railAutoFold : railOverride;
  const finderEnabled = !!listFiles;
  const finderActive = variant === 'tile' || active;
  const [finderOpen, setFinderOpen] = useState(false);
  const { files: finderFiles, loading: finderLoading } = useNotebookFileIndex(listFiles, changeSignal, finderEnabled && finderActive);
  const finderReturnFocusRef = useRef<HTMLElement | null>(null);
  const openFinder = useCallback(() => {
    finderReturnFocusRef.current = document.activeElement as HTMLElement | null;
    setFinderOpen(true);
  }, []);
  useEffect(() => {
    if (!finderEnabled) return;
    return registerPaletteClaim({ container: () => dialogRef.current, open: openFinder });
  }, [finderEnabled, openFinder]);
  // preventDefault stops the WebView print dialog; Shift is excluded because Cmd+Shift+P is the global attention dock.
  const handleSurfaceKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'p') {
      event.preventDefault();
      event.stopPropagation();
      if (finderEnabled) openFinder();
    }
  }, [finderEnabled, openFinder]);
  const finderWasOpenRef = useRef(false);
  useEffect(() => {
    if (finderWasOpenRef.current && !finderOpen) {
      const prev = finderReturnFocusRef.current;
      finderReturnFocusRef.current = null;
      const container = dialogRef.current;
      if (prev && container?.contains(prev)) {
        prev.focus();
      } else {
        container?.focus();
      }
    }
    finderWasOpenRef.current = finderOpen;
  }, [finderOpen]);
  const noteType = useMemo(() => {
    if (!note || !selectedPath) return null;
    const kind = fileKind(selectedPath);
    if (kind === 'markdown') {
      const type = parseFrontmatter(note.content)?.fields.type;
      return typeof type === 'string' && type.trim() ? type.trim() : 'note';
    }
    if (kind === 'text') return 'text';
    return null;
  }, [note, selectedPath]);

  const requestClose = useCallback(async () => {
    if (dirtyRef.current) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    onClose?.();
  }, [onClose]);
  const handleEscape = useCallback(() => void requestClose(), [requestClose]);

  useEscapeStack(handleEscape, variant === 'modal' && active);
  useEscapeStack(() => setFinderOpen(false), variant === 'modal' && active && finderOpen);
  useEscapeStack(() => { editorRef.current?.closeSearchPanel(); }, active && searchOpen);

  const loadFile = useCallback(async (path: string, prefetched?: FsReadResult) => {
    if (dirtyRef.current && selectedPathRef.current && selectedPathRef.current !== path) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    const seq = ++loadSeqRef.current;
    setSelectedPath(path);
    onOpenFile?.(path);
    setChiefSel(null);
    setBacklinks([]);
    setBacklinksLoading(false);

    if (isBinaryPath(path)) {
      setNote(null);
      setDraft('');
      setNoteError(null);
      setNoteLoading(false);
      return;
    }

    setNoteError(null);
    if (prefetched) {
      setNote(prefetched);
      setDraft(prefetched.content);
      setNoteLoading(false);
    } else {
      setNoteLoading(true);
      void readFile(path)
        .then((value) => {
          if (loadSeqRef.current !== seq) return;
          setNote(value);
          setDraft(value.content);
          setNoteLoading(false);
        })
        .catch((err) => {
          if (loadSeqRef.current !== seq) return;
          setNote(null);
          setDraft('');
          setNoteError(err instanceof Error ? err.message : 'Could not read this file');
          setNoteLoading(false);
        });
    }
    if (isMarkdownPath(path) && backlinksNotebook) {
      setBacklinksLoading(true);
      void backlinksNotebook(path)
        .then((entries) => {
          if (loadSeqRef.current !== seq) return;
          setBacklinks(entries);
          setBacklinksLoading(false);
        })
        .catch(() => {
          if (loadSeqRef.current !== seq) return;
          setBacklinks([]);
          setBacklinksLoading(false);
        });
    }
  }, [readFile, backlinksNotebook, onOpenFile]);

  const clearSelection = useCallback(() => {
    loadSeqRef.current += 1;
    setSelectedPath(null);
    setNote(null);
    setDraft('');
    setNoteError(null);
    setNoteLoading(false);
    setBacklinks([]);
    setBacklinksLoading(false);
  }, []);

  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [conflict, setConflict] = useState<{ currentHash?: string } | null>(null);
  const [justSaved, setJustSaved] = useState(false);
  const draftRef = useRef('');
  draftRef.current = draft;
  const noteRef = useRef<FsReadResult | null>(null);
  noteRef.current = note;
  const dirty = note ? draft !== note.content : false;
  const dirtyRef = useRef(false);
  dirtyRef.current = dirty;

  const [chiefSel, setChiefSel] = useState<LiveSelection | null>(null);
  const [sendingToChief, setSendingToChief] = useState(false);
  const [chiefStatus, setChiefStatus] = useState<{ text: string; error: boolean } | null>(null);

  const writeBuffer = useCallback(async (baseHash: string, content: string): Promise<PersistOutcome> => {
    const path = selectedPathRef.current;
    if (!path) return 'noop';
    // Freeze (never bump) the load token: the bytes still reach disk, they just aren't stamped onto the file now shown.
    const seq = loadSeqRef.current;
    setSaving(true);
    setSaveError(null);
    try {
      const res = await writeFile(path, content, baseHash || undefined);
      const superseded = loadSeqRef.current !== seq || selectedPathRef.current !== path;
      if (res.conflict) {
        if (!superseded) setConflict({ currentHash: res.currentHash });
        return 'conflict';
      }
      if (!superseded) {
        setConflict(null);
        setNote({ path, content, hash: res.hash ?? '' });
        setJustSaved(true);
      }
      return 'saved';
    } catch (err) {
      if (loadSeqRef.current === seq && selectedPathRef.current === path) {
        setSaveError(err instanceof Error ? err.message : 'Could not save this file');
      }
      return 'error';
    } finally {
      setSaving(false);
    }
  }, [writeFile]);

  const persist = useCallback(async (): Promise<PersistOutcome> => {
    const current = noteRef.current;
    if (!current) return 'noop';
    const content = draftRef.current;
    if (content === current.content) return 'noop';
    return writeBuffer(current.hash, content);
  }, [writeBuffer]);
  persistRef.current = persist;

  useImperativeHandle(ref, () => ({
    flushPendingSave: () => persistRef.current?.() ?? Promise.resolve('noop'),
  }), []);

  const reloadFromDisk = useCallback(async () => {
    const path = selectedPathRef.current;
    if (!path) return;
    // Freeze the load token, or a slow reload of A stamps its content onto B.
    const seq = loadSeqRef.current;
    setConflict(null);
    setSaveError(null);
    try {
      const fresh = await readFile(path);
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setNote(fresh);
      setDraft(fresh.content);
      editorRef.current?.focus();
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setSaveError(err instanceof Error ? err.message : 'Could not reload this file');
    }
  }, [readFile]);

  const refreshOpenFile = useCallback(async () => {
    const path = selectedPathRef.current;
    if (!path || isBinaryPath(path)) return;
    // Freeze (do NOT bump) the token, so a navigation mid-read drops this refresh.
    const seq = loadSeqRef.current;
    let fresh: FsReadResult;
    try {
      fresh = await readFile(path);
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setNote(null);
      setDraft('');
      setNoteError(err instanceof Error ? err.message : 'Could not read this file');
      return;
    }
    if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
    // Unchanged: skipping every setState is the point — scroll and selection hold.
    if (noteRef.current && fresh.hash === noteRef.current.hash) return;
    if (isMarkdownPath(path)) {
      editorRef.current?.applyExternalContent(fresh.content);
    }
    setNote(fresh);
    setDraft(fresh.content);
    setNoteError(null);
    if (isMarkdownPath(path) && backlinksNotebook) {
      setBacklinksLoading(true);
      void backlinksNotebook(path)
        .then((entries) => {
          if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
          setBacklinks(entries);
          setBacklinksLoading(false);
        })
        .catch(() => {
          if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
          setBacklinks([]);
          setBacklinksLoading(false);
        });
    }
  }, [readFile, backlinksNotebook]);

  const sendSelectionToChief = useCallback(async () => {
    if (!chiefSel || !sendToChief) return;
    const path = selectedPathRef.current ?? undefined;
    const seq = loadSeqRef.current;
    setSendingToChief(true);
    try {
      await sendToChief(chiefSel.text, path);
      if (loadSeqRef.current !== seq || (selectedPathRef.current ?? undefined) !== path) return;
      setChiefSel(null);
      setChiefStatus({ text: "Added to chief's inbox", error: false });
    } catch (err) {
      if (loadSeqRef.current !== seq || (selectedPathRef.current ?? undefined) !== path) return;
      setChiefStatus({ text: err instanceof Error ? err.message : 'Could not send to chief', error: true });
    } finally {
      setSendingToChief(false);
    }
  }, [chiefSel, sendToChief]);

  useEffect(() => {
    if (!active) return;
    setChiefStatus(null);
    setChiefSel(null);
    setJustSaved(false);
    let cancelled = false;
    void (async () => {
      if (variant === 'tile') {
        const seed = initialPath ?? null;
        if (!seed) {
          if (!cancelled) {
            clearSelection();
            if (finderEnabled) setFinderOpen(true);
          }
          return;
        }
        if (isBinaryPath(seed)) {
          if (!cancelled) void loadFile(seed);
          return;
        }
        try {
          const res = await readFile(seed);
          if (!cancelled) void loadFile(seed, res);
        } catch {
          if (!cancelled) clearSelection();
        }
        return;
      }
      if (initialPath) {
        try {
          const res = await readFile(initialPath);
          if (!cancelled) void loadFile(initialPath, res);
          return;
        } catch {
        }
      }
      const current = selectedPathRef.current;
      if (current) {
        // Preserved WITHOUT reading: a probe leaks the fs_read the gate prevents.
        if (isBinaryPath(current)) {
          if (!cancelled) void loadFile(current);
          return;
        }
        try {
          const res = await readFile(current);
          if (!cancelled) void loadFile(current, res);
          return;
        } catch {
        }
      }
      for (const candidate of PREFERRED_FIRST) {
        if (cancelled) return;
        try {
          const res = await readFile(candidate);
          if (!cancelled) void loadFile(candidate, res);
          return;
        } catch {
        }
      }
      try {
        const root = await listDir('');
        if (cancelled) return;
        const firstFile = root.find((e) => !e.isDir);
        if (firstFile) void loadFile(firstFile.path);
        else clearSelection();
      } catch {
        if (!cancelled) clearSelection();
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  useEffect(() => {
    if (!active || changeSignal === 0) return;
    if (dirtyRef.current) return;
    void refreshOpenFile();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeSignal]);

  useEffect(() => {
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    setChiefSel(null);
    setChiefStatus(null);
    editorRef.current?.closeSearchPanel();
    setSearchOpen(false);
  }, [selectedPath]);

  useEffect(() => {
    if (!note || noteLoading || saving || conflict) return;
    if (draft === note.content) return;
    const timer = window.setTimeout(() => {
      void persist();
    }, AUTOSAVE_DELAY_MS);
    return () => window.clearTimeout(timer);
  }, [draft, note, noteLoading, saving, conflict, persist]);

  useEffect(() => {
    if (!chiefStatus) return;
    const timer = window.setTimeout(() => setChiefStatus(null), chiefStatus.error ? 6000 : 3000);
    return () => window.clearTimeout(timer);
  }, [chiefStatus]);

  // Scroll is captured, not bubbled, or a nested code-block scroller slips past and strands the button.
  useEffect(() => {
    if (!chiefSel) return;
    const clear = () => setChiefSel(null);
    window.addEventListener('resize', clear);
    document.addEventListener('scroll', clear, true);
    return () => {
      window.removeEventListener('resize', clear);
      document.removeEventListener('scroll', clear, true);
    };
  }, [chiefSel]);

  useEffect(() => {
    if (!justSaved) return;
    const timer = window.setTimeout(() => setJustSaved(false), 2500);
    return () => window.clearTimeout(timer);
  }, [justSaved]);

  const scrollToAnchor = useCallback((anchor: string) => {
    const wanted = headingSlug(anchor);
    const heading = parseOutline(draftRef.current).find(
      (h) => headingSlug(h.text) === wanted || h.text.toLowerCase() === anchor.toLowerCase(),
    );
    if (heading) editorRef.current?.scrollToPos(heading.pos);
  }, []);

  const handleFollowLink = useCallback((href: string) => {
    const resolved = resolveNotebookLink(href, noteDir(selectedPathRef.current ?? ''));
    if (resolved.kind === 'note') {
      if (resolved.path === selectedPathRef.current && resolved.anchor) {
        scrollToAnchor(resolved.anchor);
      } else {
        void loadFile(resolved.path);
      }
    } else if (resolved.kind === 'fragment') {
      scrollToAnchor(resolved.anchor);
    } else if (resolved.href) {
      window.open(resolved.href, '_blank', 'noreferrer');
    }
  }, [loadFile]);

  const handleSelectionChange = useCallback((selection: LiveSelection | null) => {
    if (!sendToChief) return;
    setChiefSel(selection);
  }, [sendToChief]);

  const resolveImageSrc = useCallback(async (src: string) => {
    const path = notebookLinkPath(src, noteDir(selectedPathRef.current ?? ''));
    if (!path) return null;
    try {
      const asset = await readAsset(path);
      return `data:${asset.mimeType};base64,${asset.dataBase64}`;
    } catch {
      return null;
    }
  }, [readAsset]);

  const selectedIsMarkdown = selectedPath ? isMarkdownPath(selectedPath) : false;
  const outline = useMemo(
    () => (selectedIsMarkdown ? parseOutline(draft) : []),
    [selectedIsMarkdown, draft],
  );

  if (variant === 'modal' && !active) return null;

  const selectedKind = selectedPath ? fileKind(selectedPath) : null;
  const showBinaryPlaceholder = selectedPath !== null && selectedKind === 'binary';
  const showRail = selectedKind === 'markdown' && !!note && !!backlinksNotebook;
  const saveStatus = saveError
    ? null
    : saving
      ? 'Saving…'
      : dirty
        ? 'Unsaved…'
        : justSaved
          ? 'Saved'
          : null;

  const body = (
    <div
      ref={bodyRef}
      className={`notebook-browser-body${showRail ? ' has-rail' : ''}${treeFolded ? ' tree-folded' : ''}${showRail && railFolded ? ' rail-folded' : ''}`}
    >
      {/* `inert` while folded keeps a keyboard user out of a collapsed pane's controls
          (aria-hidden alone leaves them focusable). */}
      <aside
        className="notebook-browser-list"
        aria-label="Notebook files"
        aria-hidden={treeFolded}
        inert={treeFolded}
      >
        <FileTree
          listDir={listDir}
          selectedPath={selectedPath}
          onSelectFile={(path) => void loadFile(path)}
          changeSignal={changeSignal}
        />
      </aside>

      <main className="notebook-browser-document">
        {noteLoading && !note && (
          <div className="notebook-browser-document-state">Loading…</div>
        )}
        {!noteLoading && noteError && (
          <div className="notebook-browser-document-state">
            <NotebookIcon />
            <h2>File unavailable</h2>
            <p>{noteError}</p>
          </div>
        )}
        {!noteLoading && !noteError && showBinaryPlaceholder && (
          <div className="notebook-browser-document-state">
            <NotebookIcon />
            <h2>Preview not available</h2>
            <p>{basename(selectedPath)} can't be opened here yet.</p>
            <p className="notebook-browser-document-subtle">{selectedPath}</p>
          </div>
        )}
        {!noteError && !showBinaryPlaceholder && note && (
          <>
            <div className="notebook-browser-document-meta">
              <div className="notebook-browser-document-titles">
                <div className="notebook-browser-document-titlerow">
                  <h2>{basename(note.path)}</h2>
                  {noteType && (
                    <span className={`notebook-browser-kind-badge${noteType === 'journal' ? ' is-journal' : ' is-note'}`}>
                      {noteType}
                    </span>
                  )}
                </div>
                <p>{note.path}</p>
              </div>
              <div className="notebook-browser-document-actions">
                {chiefStatus && (
                  <span
                    className={`notebook-browser-chief-status${chiefStatus.error ? ' is-error' : ''}`}
                    role="status"
                  >
                    {chiefStatus.text}
                  </span>
                )}
                {saveStatus && (
                  <span className="notebook-browser-save-status" role="status">{saveStatus}</span>
                )}
              </div>
            </div>
            <div className="notebook-browser-live">
              {conflict && (
                <div className="notebook-browser-editor-conflict" role="alert">
                  <span>
                    {conflict.currentHash
                      ? 'This file changed on disk since you opened it.'
                      : 'This file was deleted on disk since you opened it.'}
                  </span>
                  <div className="notebook-browser-editor-conflict-actions">
                    <button type="button" onClick={() => void reloadFromDisk()} disabled={saving}>
                      Reload from disk
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        void (async () => {
                          await writeBuffer(conflict.currentHash ?? '', draft);
                          editorRef.current?.focus();
                        })();
                      }}
                      disabled={saving}
                    >
                      Overwrite anyway
                    </button>
                  </div>
                </div>
              )}
              {saveError && (
                <p className="notebook-browser-editor-error" role="alert">{saveError}</p>
              )}
              <div className="notebook-browser-live-editor">
                {selectedKind === 'markdown' ? (
                  <LiveMarkdownEditor
                    ref={editorRef}
                    value={draft}
                    onChange={setDraft}
                    onFollowLink={handleFollowLink}
                    onSelectionChange={handleSelectionChange}
                    existsFile={existsFile}
                    resolveImageSrc={resolveImageSrc}
                    revalidateSignal={changeSignal}
                    notePath={selectedPath ?? ''}
                    ariaLabel="Note"
                    onSearchOpenChange={setSearchOpen}
                  />
                ) : (
                  <textarea
                    className="notebook-browser-plain-editor"
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    spellCheck={false}
                    aria-label="File contents"
                  />
                )}
              </div>
            </div>
          </>
        )}
        {!noteLoading && !noteError && !showBinaryPlaceholder && !note && (
          <div className="notebook-browser-document-state">
            <NotebookIcon />
            <h2>Nothing selected</h2>
            {finderEnabled ? (
              <>
                <p>Find a note, or pick one from the tree.</p>
                <button
                  type="button"
                  className="notebook-finder-open-button"
                  onClick={openFinder}
                >
                  <span>Find a note</span><kbd>⌘P</kbd>
                </button>
              </>
            ) : (
              <p>Choose a file from the tree to read it.</p>
            )}
          </div>
        )}
      </main>

      {showRail && (
        <aside
          className="notebook-browser-rail"
          aria-label="Context"
          aria-hidden={railFolded}
          inert={railFolded}
        >
          <section className="notebook-browser-rail-section">
            <button
              type="button"
              className="notebook-browser-rail-toggle"
              aria-expanded={outlineOpen}
              onClick={() => setOutlineOpen((open) => !open)}
            >
              <span className={`notebook-browser-rail-caret${outlineOpen ? ' is-open' : ''}`} aria-hidden="true" />
              <span className="notebook-browser-rail-title">Outline</span>
              {outlineOpen && outline.length > 0 && (
                <span className="notebook-browser-rail-count">{outline.length}</span>
              )}
            </button>
            {outlineOpen && (
              <div className="notebook-browser-rail-body">
                {outline.length === 0 ? (
                  <p className="notebook-browser-rail-empty">No headings.</p>
                ) : (
                  <ul className="notebook-browser-outline">
                    {outline.map((heading) => (
                      <li key={`${heading.line}:${heading.pos}`}>
                        <button
                          type="button"
                          className={`notebook-browser-outline-item is-h${heading.level}`}
                          onClick={() => editorRef.current?.scrollToPos(heading.pos)}
                          title={heading.text}
                        >
                          {heading.text}
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
          </section>

          <section className="notebook-browser-rail-section">
            <button
              type="button"
              className="notebook-browser-rail-toggle"
              aria-expanded={backlinksOpen}
              onClick={() => setBacklinksOpen((open) => !open)}
            >
              <span className={`notebook-browser-rail-caret${backlinksOpen ? ' is-open' : ''}`} aria-hidden="true" />
              <span className="notebook-browser-rail-title">Backlinks</span>
              {backlinksOpen && !backlinksLoading && backlinks.length > 0 && (
                <span className="notebook-browser-rail-count">{backlinks.length}</span>
              )}
            </button>
            {backlinksOpen && (
              <div className="notebook-browser-rail-body">
                {backlinksLoading ? (
                  <p className="notebook-browser-rail-empty">Finding backlinks…</p>
                ) : backlinks.length === 0 ? (
                  <p className="notebook-browser-rail-empty">No other note links here.</p>
                ) : (
                  <ul className="notebook-browser-backlinks">
                    {backlinks.map((entry) => (
                      <li key={entry.path}>
                        <button
                          type="button"
                          className="notebook-browser-backlink"
                          onClick={() => void loadFile(entry.path)}
                          title={entry.path}
                          aria-label={entry.title || basename(entry.path)}
                        >
                          <span className="notebook-browser-backlink-title">{entry.title || basename(entry.path)}</span>
                          <span className="notebook-browser-backlink-path">{entry.path}</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
          </section>
        </aside>
      )}

      <button
        type="button"
        className="notebook-browser-fold notebook-browser-fold-tree"
        aria-label={treeFolded ? 'Show file tree' : 'Hide file tree'}
        aria-expanded={!treeFolded}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setTreeOverride(!treeFolded)}
      >
        {treeFolded ? '›' : '‹'}
      </button>
      {showRail && (
        <button
          type="button"
          className="notebook-browser-fold notebook-browser-fold-rail"
          aria-label={railFolded ? 'Show context rail' : 'Hide context rail'}
          aria-expanded={!railFolded}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => setRailOverride(!railFolded)}
        >
          {railFolded ? '‹' : '›'}
        </button>
      )}
    </div>
  );

  const finderOverlay = finderOpen ? (
    <NotebookFinder
      files={finderFiles}
      loading={finderLoading}
      onPick={(path) => { void loadFile(path); setFinderOpen(false); }}
      onClose={() => setFinderOpen(false)}
    />
  ) : null;

  const floatingChief = chiefSel && sendToChief ? (
    <button
      type="button"
      className="notebook-browser-send-chief"
      style={{ top: chiefSel.top, left: chiefSel.left }}
      // Keep the selection intact so onClick reads it uncollapsed.
      onMouseDown={(event) => event.preventDefault()}
      onClick={() => void sendSelectionToChief()}
      disabled={sendingToChief}
    >
      {sendingToChief ? 'Sending…' : 'Send to chief'}
    </button>
  ) : null;

  if (variant === 'tile') {
    return (
      <div
        ref={dialogRef}
        tabIndex={-1}
        className="notebook-surface notebook-surface-tile"
        onKeyDown={handleSurfaceKeyDown}
      >
        {body}
        {floatingChief}
        {finderOverlay}
      </div>
    );
  }

  return (
    <div className="notebook-browser-shell">
      <FocusTrap focusTrapOptions={{ escapeDeactivates: false, initialFocus: () => dialogRef.current ?? false }}>
        <div ref={dialogRef} tabIndex={-1} className="notebook-browser" role="dialog" aria-modal="true" aria-labelledby="notebook-browser-title" onKeyDown={handleSurfaceKeyDown}>
          <header className="notebook-browser-header">
            <div className="notebook-browser-heading">
              <NotebookIcon />
              <div>
                <span className="notebook-browser-eyebrow">Knowledge base</span>
                <h1 id="notebook-browser-title">Notebook</h1>
              </div>
            </div>
            <div className="notebook-browser-chrome">
              {chiefActive !== undefined && (
                <span
                  className={`notebook-browser-chief-pulse${chiefActive ? ' is-active' : ''}`}
                  role="status"
                >
                  <span className="notebook-browser-chief-dot" aria-hidden="true" />
                  chief: {chiefActive ? 'active' : 'idle'}
                </span>
              )}
              <button type="button" className="notebook-browser-close" onClick={() => void requestClose()}>
                <span>Close</span><kbd>esc</kbd>
              </button>
            </div>
          </header>
          {body}
          {floatingChief}
          {finderOverlay}
        </div>
      </FocusTrap>
    </div>
  );
});

function basename(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return name.endsWith('.md') ? name.slice(0, -3) : name;
}

function NotebookIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3.5h11a1 1 0 0 1 1 1V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z" />
      <path d="M9 3.5V21M12 8h4M12 11.5h4" />
    </svg>
  );
}
