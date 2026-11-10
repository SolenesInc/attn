import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode, RefObject } from 'react';
import type { ReviewBundle, ReviewFileResultMessage, ReviewRow } from '../../types/generated';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useTileWindow } from '../../tileWindow';
import { isDomInstance } from '../../utils/realm';
import { rowKey } from './CodeRows';
import { changeStarts, FileView, rowIndexOf, type OpenFile } from './FileView';
import { ReviewKeyLine, ReviewKeySheet } from './ReviewKeys';
import { CURSOR_CLASS, ReviewReaderContext, type FileTarget, type ReviewReaderContextValue, type StopExpansion } from './reviewContext';
import { SearchPanel } from './SearchPanel';
import './ReviewReader.css';

const MORE_LINES = 10;
const PAGE_ROWS = 20;

function isEditable(node: EventTarget | null): boolean {
  if (!isDomInstance(node, 'Element')) return false;
  return node.matches('input, textarea, select, [role="textbox"]') || (node as HTMLElement).isContentEditable;
}

function isPrintable(event: KeyboardEvent): boolean {
  return event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey;
}

function focusIsAround(root: HTMLElement, active: Element | null): boolean {
  if (!active || active === root.ownerDocument.body) return false;
  return root.contains(active) || active.contains(root);
}

function useFocusAround(rootRef: RefObject<HTMLElement | null>, tileWindow: Window): boolean {
  const [focused, setFocused] = useState(false);
  useEffect(() => {
    const update = () => {
      const root = rootRef.current;
      setFocused(root !== null && focusIsAround(root, tileWindow.document.activeElement));
    };
    update();
    tileWindow.addEventListener('focusin', update);
    tileWindow.addEventListener('focusout', update);
    return () => {
      tileWindow.removeEventListener('focusin', update);
      tileWindow.removeEventListener('focusout', update);
    };
  }, [rootRef, tileWindow]);
  return focused;
}

function cursorKeyOf(element: Element): string {
  const key = element.getAttribute('data-cursor-key');
  return key ?? `block:${element.getAttribute('data-block-id')}`;
}

function documentTargets(doc: HTMLElement | null): HTMLElement[] {
  if (!doc) return [];
  return Array.from(doc.querySelectorAll<HTMLElement>('[data-review-cursor], [data-block-id]')).filter((element) => (
    element.hasAttribute('data-review-cursor') || element.querySelector('[data-block-id]') === null
  ));
}

function findTarget(doc: HTMLElement | null, key: string | null): HTMLElement | null {
  if (!doc || !key) return null;
  if (key.startsWith('block:')) return doc.querySelector<HTMLElement>(`[data-block-id="${CSS.escape(key.slice(6))}"]`);
  return doc.querySelector<HTMLElement>(`[data-cursor-key="${CSS.escape(key)}"]`);
}

/** The rows a stop shows, widened by a number of lines on each side, taken from the whole file. */
function widen(shown: readonly ReviewRow[], file: readonly ReviewRow[], lines: number): ReviewRow[] | null {
  if (shown.length === 0) return null;
  const at = (row: ReviewRow) => file.findIndex((candidate) => rowKey(candidate) === rowKey(row));
  const first = at(shown[0]);
  const last = at(shown[shown.length - 1]);
  if (first < 0 || last < 0) return null;
  return file.slice(Math.max(0, first - lines), Math.min(file.length, last + lines + 1));
}

export function ReviewReader({
  bundle,
  host,
  document,
  content,
  rootRef,
  children,
}: {
  bundle: ReviewBundle;
  host?: string;
  document: string;
  content: string;
  rootRef: RefObject<HTMLDivElement | null>;
  children: ReactNode;
}) {
  const daemon = useOptionalDaemonApi();
  const sendReviewFile = daemon?.sendReviewFile;
  const sendReviewSearch = daemon?.sendReviewSearch;
  const tileWindow = useTileWindow();
  const docRef = useRef<HTMLDivElement | null>(null);
  const [cursorKey, setCursorKey] = useState<string | null>(null);
  const [expansions, setExpansions] = useState<ReadonlyMap<number, StopExpansion>>(new Map());
  const [files, setFiles] = useState<OpenFile[]>([]);
  const [filled, setFilled] = useState(false);
  const [search, setSearch] = useState<{ query: string } | null>(null);
  const [help, setHelp] = useState(false);
  const cursorElementRef = useRef<HTMLElement | null>(null);
  const cursorIndexRef = useRef(0);
  const scrolledKeyRef = useRef<string | null>(null);
  const scrollModeRef = useRef<ScrollLogicalPosition>('nearest');
  const fileRowsRef = useRef(new Map<string, Promise<ReviewRow[]>>());
  const fileIdRef = useRef(0);
  const openFileView = files.length > 0 ? files[files.length - 1] : null;

  useEffect(() => {
    fileRowsRef.current.clear();
    setExpansions(new Map());
  }, [bundle]);

  const readFile = useCallback((path: string): Promise<ReviewFileResultMessage> => {
    if (!sendReviewFile) return Promise.reject(new Error('Not connected to the daemon'));
    return sendReviewFile(host, document, path);
  }, [document, host, sendReviewFile]);

  const wholeFile = useCallback((path: string): Promise<ReviewRow[]> => {
    let rows = fileRowsRef.current.get(path);
    if (!rows) {
      rows = readFile(path).then((answer) => answer.rows ?? []);
      rows.catch(() => fileRowsRef.current.delete(path));
      fileRowsRef.current.set(path, rows);
    }
    return rows;
  }, [readFile]);

  const openFile = useCallback((target: FileTarget, options?: { fill?: boolean }) => {
    setSearch(null);
    if (options?.fill) setFilled(true);
    const id = ++fileIdRef.current;
    setFiles((stack) => [...stack, { id, target, rows: null, cursor: 0 }]);
    const settle = (next: Partial<OpenFile>) => setFiles((stack) => stack.map((open) => (open.id === id ? { ...open, ...next } : open)));
    void readFile(target.path).then(
      (answer) => {
        const rows = answer.rows ?? [];
        settle({ rows, problem: answer.problem, cursor: rowIndexOf(rows, target.line ?? target.range?.[0]) });
      },
      (error: unknown) => settle({ error: error instanceof Error ? error.message : String(error) }),
    );
  }, [readFile]);

  const openSearch = useCallback((query = '') => setSearch({ query }), []);

  const closeFileView = useCallback(() => {
    setFiles([]);
    setFilled(false);
  }, []);

  const back = useCallback(() => {
    setFiles((stack) => stack.slice(0, -1));
    if (files.length <= 1) setFilled(false);
  }, [files.length]);

  const moveFileCursor = useCallback((to: (file: OpenFile) => number) => {
    setFiles((stack) => {
      const top = stack[stack.length - 1];
      if (!top?.rows || top.rows.length === 0) return stack;
      const cursor = Math.max(0, Math.min(top.rows.length - 1, to(top)));
      return [...stack.slice(0, -1), { ...top, cursor }];
    });
  }, []);

  const setCursor = useCallback((element: HTMLElement | null, scroll: ScrollLogicalPosition = 'nearest') => {
    if (!element) return;
    scrollModeRef.current = scroll;
    setCursorKey(cursorKeyOf(element));
  }, []);

  useLayoutEffect(() => {
    cursorElementRef.current?.classList.remove(CURSOR_CLASS);
    cursorElementRef.current = null;
    if (cursorKey === null) return;
    const element = findTarget(docRef.current, cursorKey);
    if (!element) {
      const targets = documentTargets(docRef.current);
      const nearest = targets[Math.min(cursorIndexRef.current, targets.length - 1)];
      if (nearest) setCursorKey(cursorKeyOf(nearest));
      return;
    }
    element.classList.add(CURSOR_CLASS);
    cursorElementRef.current = element;
    cursorIndexRef.current = Math.max(0, documentTargets(docRef.current).indexOf(element));
    if (scrolledKeyRef.current !== cursorKey) {
      scrolledKeyRef.current = cursorKey;
      element.scrollIntoView?.({ block: scrollModeRef.current });
    }
    scrollModeRef.current = 'nearest';
  }, [cursorKey, content, bundle, expansions]);

  const currentTarget = useCallback(() => findTarget(docRef.current, cursorKey), [cursorKey]);

  const moveLine = useCallback((delta: number) => {
    const targets = documentTargets(docRef.current);
    if (targets.length === 0) return;
    const at = targets.findIndex((element) => cursorKeyOf(element) === cursorKey);
    setCursor(targets[at < 0 ? 0 : Math.max(0, Math.min(targets.length - 1, at + delta))]);
  }, [cursorKey, setCursor]);

  const moveStop = useCallback((delta: number) => {
    const doc = docRef.current;
    if (!doc) return;
    const stops = Array.from(doc.querySelectorAll<HTMLElement>('[data-review-stop-head]'));
    const current = currentTarget();
    const inStop = current?.closest<HTMLElement>('[data-review-stop-head]') ?? null;
    let next: HTMLElement | undefined;
    if (delta > 0) {
      next = stops.find((stop) => stop !== inStop && current !== null && (current.compareDocumentPosition(stop) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0)
        ?? (current === null ? stops[0] : undefined);
    } else {
      const before = stops.filter((stop) => stop !== inStop && current !== null && (current.compareDocumentPosition(stop) & Node.DOCUMENT_POSITION_PRECEDING) !== 0);
      next = before[before.length - 1];
    }
    const firstRow = next?.querySelector<HTMLElement>('[data-review-cursor]');
    if (firstRow) setCursor(firstRow, 'start');
  }, [currentTarget, setCursor]);

  const stopOf = useCallback((element: Element | null) => {
    const line = Number(element?.closest('[data-review-stop-head]')?.getAttribute('data-review-stop-head'));
    return bundle.stops.find((stop) => stop.heading_line === line);
  }, [bundle]);

  const targetAtCursor = useCallback((): FileTarget | 'search' | null => {
    const element = currentTarget();
    if (!element) return null;
    const searchPrefix = element.getAttribute('data-search');
    if (searchPrefix) {
      openSearch(searchPrefix);
      return 'search';
    }
    const path = element.getAttribute('data-path');
    if (!path) return null;
    const line = Number(element.getAttribute('data-line')) || undefined;
    const stop = stopOf(element);
    const range: [number, number] | undefined = stop?.now_from ? [stop.now_from, stop.now_to] : undefined;
    return { path, line: line ?? range?.[0], range };
  }, [currentTarget, openSearch, stopOf]);

  const openAtCursor = useCallback((fill: boolean) => {
    const target = targetAtCursor();
    if (target && target !== 'search') openFile(target, { fill });
  }, [openFile, targetAtCursor]);

  const expandAtCursor = useCallback(() => {
    const stop = stopOf(currentTarget());
    if (!stop) return;
    const shown = expansions.get(stop.heading_line)?.rows ?? stop.rows;
    void wholeFile(stop.path).then(
      (file) => {
        const rows = widen(shown, file, MORE_LINES);
        if (rows) setExpansions((current) => new Map(current).set(stop.heading_line, { rows }));
      },
      (error: unknown) => setExpansions((current) => new Map(current).set(stop.heading_line, { rows: shown, error: error instanceof Error ? error.message : String(error) })),
    );
  }, [currentTarget, expansions, stopOf, wholeFile]);

  const searchOpen = search !== null;
  const searchWasOpenRef = useRef(false);
  useEffect(() => {
    if (searchWasOpenRef.current && !searchOpen) rootRef.current?.focus({ preventScroll: true });
    searchWasOpenRef.current = searchOpen;
  }, [rootRef, searchOpen]);

  const focused = useFocusAround(rootRef, tileWindow);
  useEscapeStack(() => setSearch(null), focused && searchOpen);
  useEscapeStack(() => setHelp(false), focused && help);
  useEscapeStack(closeFileView, focused && openFileView !== null && !searchOpen && !help);

  useEffect(() => {
    const root = rootRef.current;
    if (!root) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) return;
      const active = tileWindow.document.activeElement;
      if (!focusIsAround(root, active)) return;
      if (isEditable(event.target) || isEditable(active)) return;
      const selection = tileWindow.getSelection();
      if (selection && !selection.isCollapsed && root.contains(selection.anchorNode) && isPrintable(event)) return;
      const handled = handleKey(event);
      if (handled) event.preventDefault();
    };
    const handleKey = (event: KeyboardEvent): boolean => {
      if (event.ctrlKey && !event.metaKey && !event.altKey && event.key === 'o') {
        if (openFileView) back();
        return openFileView !== null;
      }
      if (event.ctrlKey || event.metaKey || event.altKey) return false;
      if (help) {
        if (event.key === '?') setHelp(false);
        return event.key === '?';
      }
      const fileRows = openFileView?.rows;
      if (openFileView) {
        switch (event.key) {
          case 'ArrowDown': moveFileCursor((file) => file.cursor + 1); return true;
          case 'ArrowUp': moveFileCursor((file) => file.cursor - 1); return true;
          case 'PageDown': moveFileCursor((file) => file.cursor + PAGE_ROWS); return true;
          case 'PageUp': moveFileCursor((file) => file.cursor - PAGE_ROWS); return true;
          case 'ArrowRight': if (fileRows) moveFileCursor((file) => changeStarts(fileRows).find((start) => start > file.cursor) ?? file.cursor); return true;
          case 'ArrowLeft': if (fileRows) moveFileCursor((file) => [...changeStarts(fileRows)].reverse().find((start) => start < file.cursor) ?? file.cursor); return true;
          case 'z': setFilled((value) => !value); return true;
        }
      } else {
        switch (event.key) {
          case 'ArrowDown': moveLine(1); return true;
          case 'ArrowUp': moveLine(-1); return true;
          case 'ArrowRight': moveStop(1); return true;
          case 'ArrowLeft': moveStop(-1); return true;
          case 'Enter': openAtCursor(false); return true;
          case 'z': openAtCursor(true); return true;
          case 'e': expandAtCursor(); return true;
        }
      }
      switch (event.key) {
        case '/': openSearch(); return true;
        case '?': setHelp(true); return true;
      }
      return false;
    };
    tileWindow.addEventListener('keydown', onKeyDown);
    return () => tileWindow.removeEventListener('keydown', onKeyDown);
  }, [back, expandAtCursor, help, moveFileCursor, moveLine, moveStop, openAtCursor, openFileView, openSearch, rootRef, tileWindow]);

  const onDocClick = useCallback((event: React.MouseEvent) => {
    const target = event.target as Element;
    const element = target.closest<HTMLElement>('[data-review-cursor], [data-block-id]');
    if (element && docRef.current?.contains(element)) setCursor(element);
  }, [setCursor]);

  const searchRepository = useCallback((query: string) => {
    if (!sendReviewSearch) return Promise.reject(new Error('Not connected to the daemon'));
    return sendReviewSearch(host, document, query);
  }, [document, host, sendReviewSearch]);

  const context = useMemo<ReviewReaderContextValue>(
    () => ({ bundle, expansions, openFile, openSearch }),
    [bundle, expansions, openFile, openSearch],
  );

  return (
    <ReviewReaderContext.Provider value={context}>
      <div className="review-doc" ref={docRef} onClick={onDocClick}>
        {children}
      </div>
      {openFileView ? (
        <FileView
          file={openFileView}
          filled={filled}
          canGoBack={files.length > 1}
          onBack={back}
          onClose={closeFileView}
          onPick={(index) => moveFileCursor(() => index)}
        />
      ) : null}
      {search ? <SearchPanel initialQuery={search.query} search={searchRepository} onPick={(target) => openFile(target)} /> : null}
      {help ? <ReviewKeySheet /> : null}
      <ReviewKeyLine fileOpen={openFileView !== null} />
    </ReviewReaderContext.Provider>
  );
}
