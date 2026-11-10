import { useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode, Ref, RefObject } from 'react';
import type { ReviewBundle, ReviewFileResultMessage, ReviewFlowMessage, ReviewRow, ReviewStop } from '../../types/generated';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useTileWindow } from '../../tileWindow';
import { isDomInstance } from '../../utils/realm';
import { rowKey } from './CodeRows';
import { changeStarts, FileView, rowIndexAfterRefresh, rowIndexOf, type OpenFile } from './FileView';
import { FRAME_KEYS, hasSides, mapFlows, mapIndexOf, mapPlaceOf, mapStops, offeredFrames, openingFrame, type Frame, type MapPlace, type Side } from './frames';
import { MapFrame } from './MapFrame';
import { ReviewKeyLine, ReviewKeySheet } from './ReviewKeys';
import { CURSOR_CLASS, ReviewReaderContext, type FileTarget, type ReviewReaderContextValue, type StopExpansion } from './reviewContext';
import { SearchPanel } from './SearchPanel';
import { stopTarget } from './StopBlock';
import { fileRowsAnchor, MarkComposer, marksByLastLine, SendSheet, verdictForKey, type MarkKind, type MarkTarget, type ReviewMarks, type ReviewVerdict } from './ReviewMarks';
import './ReviewReader.css';

const MORE_LINES = 10;
const SELECTED_CLASS = 'review-selected';
const NO_MARKS: ReviewMarks['annotations'] = [];

export interface ReviewReaderHandle {
  /** Shows what a send carries and takes the verdict. */
  openSend(): void;
}
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

function stopCursorKey(stop: ReviewStop, line?: number): string | null {
  const row = (line === undefined ? undefined : stop.rows.find((candidate) => candidate.now === line)) ?? stop.rows[0];
  return row ? `stop:${stop.path}:${rowKey(row)}` : null;
}

/** Where the Walkthrough opens a stop: the place to look, else its first change, else its first line. */
function stageLine(stop: ReviewStop): number | undefined {
  const look = stop.notes.find((note) => note.look && note.now_from > 0);
  if (look) return look.now_from;
  const change = stop.rows.find((row) => row.kind === '+' && row.now !== undefined && row.now >= stop.now_from && row.now <= stop.now_to);
  return change?.now ?? (stop.now_from || undefined);
}

interface StageKey {
  path: string;
  from: number;
  index: number;
}

function stageKeyOf(bundle: ReviewBundle, stop: ReviewStop): StageKey {
  return { path: stop.path, from: stop.from, index: bundle.stops.indexOf(stop) };
}

function stopOfStage(bundle: ReviewBundle, key: StageKey | null): ReviewStop | undefined {
  if (!key || bundle.stops.length === 0) return undefined;
  return bundle.stops.find((stop) => stop.path === key.path && stop.from === key.from)
    ?? bundle.stops[Math.min(key.index, bundle.stops.length - 1)];
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

function rowText(element: Element): string {
  const text = element.querySelector('.review-row-text')?.textContent ?? '';
  return text === ' ' ? '' : text;
}

function rowOf(element: Element): ReviewRow {
  const number = (name: string) => {
    const value = Number(element.getAttribute(name));
    return value > 0 ? value : undefined;
  };
  return { kind: element.getAttribute('data-kind') ?? ' ', old: number('data-old'), now: number('data-line'), text: rowText(element) };
}

/** The targets from one cursor key to another, both included, in document order. */
function targetsBetween(doc: HTMLElement | null, fromKey: string, toKey: string): HTMLElement[] {
  const targets = documentTargets(doc);
  const from = targets.findIndex((element) => cursorKeyOf(element) === fromKey);
  const to = targets.findIndex((element) => cursorKeyOf(element) === toKey);
  if (from < 0 || to < 0) return [];
  return targets.slice(Math.min(from, to), Math.max(from, to) + 1);
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
  marks,
  handleRef,
  children,
}: {
  bundle: ReviewBundle;
  host?: string;
  document: string;
  content: string;
  rootRef: RefObject<HTMLDivElement | null>;
  marks?: ReviewMarks;
  handleRef?: Ref<ReviewReaderHandle>;
  children: ReactNode;
}) {
  const daemon = useOptionalDaemonApi();
  const sendReviewFile = daemon?.sendReviewFile;
  const sendReviewSearch = daemon?.sendReviewSearch;
  const tileWindow = useTileWindow();
  const docRef = useRef<HTMLDivElement | null>(null);
  const detailRef = useRef<HTMLDivElement | null>(null);
  const offered = useMemo(() => offeredFrames(bundle), [bundle]);
  const [chosenFrame, setChosenFrame] = useState<Frame>(() => openingFrame(bundle));
  const frame: Frame = offered.includes(chosenFrame) ? chosenFrame : 'brief';
  const sides = useMemo(() => hasSides(bundle), [bundle]);
  const [side, setSide] = useState<Side>('after');
  const flows = useMemo(() => mapFlows(bundle, side), [bundle, side]);
  const messageStops = useMemo(() => mapStops(flows), [flows]);
  const [mapPlace, setMapPlace] = useState<MapPlace>({ text: '', index: 0 });
  const mapIndex = useMemo(() => mapIndexOf(bundle, messageStops, mapPlace), [bundle, mapPlace, messageStops]);
  const [stageKey, setStageKey] = useState<StageKey | null>(null);
  const stageStop = frame === 'walkthrough' ? stopOfStage(bundle, stageKey) : undefined;
  const [cursorKey, setCursorKey] = useState<string | null>(null);
  const [expansions, setExpansions] = useState<ReadonlyMap<number, StopExpansion>>(new Map());
  const [files, setFiles] = useState<OpenFile[]>([]);
  const [filled, setFilled] = useState(false);
  const [search, setSearch] = useState<{ query: string } | null>(null);
  const [help, setHelp] = useState(false);
  const [selectFrom, setSelectFrom] = useState<string | null>(null);
  const [composer, setComposer] = useState<{ kind: MarkKind; target: MarkTarget } | null>(null);
  const [sending, setSending] = useState<{ verdict: ReviewVerdict } | null>(null);
  const [asking, setAsking] = useState<ReadonlyMap<string, string>>(new Map());
  const selectedRef = useRef<HTMLElement[]>([]);
  const annotations = marks?.annotations ?? NO_MARKS;
  const marksByLine = useMemo(() => marksByLastLine(annotations), [annotations]);
  const cursorElementRef = useRef<HTMLElement | null>(null);
  const cursorIndexRef = useRef(0);
  const scrolledKeyRef = useRef<string | null>(null);
  const scrollModeRef = useRef<ScrollLogicalPosition>('nearest');
  const fileRowsRef = useRef(new Map<string, Promise<ReviewRow[]>>());
  const fileIdRef = useRef(0);
  const readsRef = useRef(new Map<number, number>());
  const openFileView = files.length > 0 ? files[files.length - 1] : null;
  const filesRef = useRef(files);
  filesRef.current = files;
  const onStage = frame === 'walkthrough' && files.length === 1 && openFileView !== null;
  const stageCursorLine = onStage ? openFileView.rows?.[openFileView.cursor]?.now : undefined;

  const scope = useCallback((): HTMLElement | null => {
    if (frame === 'map') return detailRef.current;
    return frame === 'brief' ? docRef.current : null;
  }, [frame]);

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

  /** Reads a file into the stack entry `id`. A newer read of the same entry
   * replaces an older one, and `place` picks the cursor's row from the entry as
   * it is when the rows arrive. */
  const loadFile = useCallback((id: number, path: string, place: (rows: ReviewRow[], open: OpenFile) => number) => {
    const read = ++fileIdRef.current;
    readsRef.current.set(id, read);
    const settle = (change: (open: OpenFile) => Partial<OpenFile>) => {
      if (readsRef.current.get(id) !== read) return;
      readsRef.current.delete(id);
      setFiles((stack) => stack.map((open) => (open.id === id ? { ...open, ...change(open) } : open)));
    };
    void readFile(path).then(
      (answer) => {
        const rows = answer.rows ?? [];
        settle((open) => ({ rows, problem: answer.problem, error: undefined, cursor: place(rows, open) }));
      },
      (error: unknown) => settle(() => ({ rows: null, error: error instanceof Error ? error.message : String(error) })),
    );
  }, [readFile]);

  const openFile = useCallback((target: FileTarget, options?: { fill?: boolean }) => {
    setSearch(null);
    if (options?.fill) setFilled(true);
    const id = ++fileIdRef.current;
    setFiles((stack) => [...stack, { id, target, rows: null, cursor: 0 }]);
    loadFile(id, target.path, (rows) => rowIndexOf(rows, target.line ?? target.range?.[0]));
  }, [loadFile]);

  /** Puts a stop's file on the Walkthrough's stage. A refresh keeps the files
   * opened on top of it and the cursor in the stage's file; a move to another
   * stop drops them. */
  const showStage = useCallback((stop: ReviewStop, line?: number, refresh = false) => {
    if (!refresh) setSearch(null);
    setStageKey(stageKeyOf(bundle, stop));
    const target = stopTarget(stop, line ?? stageLine(stop));
    const id = ++fileIdRef.current;
    setFiles((stack) => {
      const previous = stack[0]?.target.path === stop.path ? stack[0] : undefined;
      const stage = refresh && previous
        ? { ...previous, id, target }
        : { id, target, rows: previous?.rows ?? null, cursor: previous?.rows ? rowIndexOf(previous.rows, target.line) : 0 };
      return refresh ? [stage, ...stack.slice(1)] : [stage];
    });
    loadFile(id, stop.path, (rows, open) => (refresh ? rowIndexAfterRefresh(rows, open) : rowIndexOf(rows, target.line)));
  }, [bundle, loadFile]);

  const openSearch = useCallback((query = '') => setSearch({ query }), []);

  const closeFileView = useCallback(() => {
    setFiles([]);
    setFilled(false);
  }, []);

  const back = useCallback(() => {
    if (frame === 'walkthrough') {
      setFiles((stack) => (stack.length > 1 ? stack.slice(0, -1) : stack));
      return;
    }
    setFiles((stack) => stack.slice(0, -1));
    if (files.length <= 1) setFilled(false);
  }, [files.length, frame]);

  const backToStage = useCallback(() => setFiles((stack) => stack.slice(0, 1)), []);

  const moveFileCursor = useCallback((to: (file: OpenFile) => number, select = false) => {
    setFiles((stack) => {
      const top = stack[stack.length - 1];
      if (!top?.rows || top.rows.length === 0) return stack;
      const cursor = Math.max(0, Math.min(top.rows.length - 1, to(top)));
      const selectFrom = select ? (top.selectFrom ?? top.cursor) : undefined;
      return [...stack.slice(0, -1), { ...top, cursor, selectFrom }];
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
    const root = scope();
    if (cursorKey === null || root === null) return;
    const element = findTarget(root, cursorKey);
    if (!element) {
      const targets = documentTargets(root);
      const nearest = targets[Math.min(cursorIndexRef.current, targets.length - 1)];
      if (nearest) setCursorKey(cursorKeyOf(nearest));
      return;
    }
    element.classList.add(CURSOR_CLASS);
    cursorElementRef.current = element;
    cursorIndexRef.current = Math.max(0, documentTargets(root).indexOf(element));
    if (scrolledKeyRef.current !== cursorKey) {
      scrolledKeyRef.current = cursorKey;
      element.scrollIntoView?.({ block: scrollModeRef.current });
    }
    scrollModeRef.current = 'nearest';
  }, [cursorKey, content, bundle, expansions, scope, mapIndex, side]);

  const selectedRows = useCallback((cursor: HTMLElement): HTMLElement[] => {
    if (cursor.getAttribute('data-review-cursor') !== 'row') return [];
    if (selectFrom === null) return [cursor];
    const path = cursor.getAttribute('data-path');
    return targetsBetween(scope(), selectFrom, cursorKeyOf(cursor))
      .filter((element) => element.getAttribute('data-review-cursor') === 'row' && element.getAttribute('data-path') === path);
  }, [scope, selectFrom]);

  useLayoutEffect(() => {
    for (const element of selectedRef.current) element.classList.remove(SELECTED_CLASS);
    const cursor = findTarget(scope(), cursorKey);
    const selected = selectFrom !== null && cursor ? selectedRows(cursor) : [];
    for (const element of selected) element.classList.add(SELECTED_CLASS);
    selectedRef.current = selected;
  }, [cursorKey, content, bundle, expansions, scope, mapIndex, side, selectFrom, selectedRows]);

  const currentTarget = useCallback(() => findTarget(scope(), cursorKey), [cursorKey, scope]);

  const moveLine = useCallback((delta: number, select = false) => {
    const targets = documentTargets(scope());
    if (targets.length === 0) return;
    const at = targets.findIndex((element) => cursorKeyOf(element) === cursorKey);
    if (select && selectFrom === null && cursorKey !== null) setSelectFrom(cursorKey);
    if (!select) setSelectFrom(null);
    setCursor(targets[at < 0 ? 0 : Math.max(0, Math.min(targets.length - 1, at + delta))]);
  }, [cursorKey, scope, selectFrom, setCursor]);

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
    return stop ? stopTarget(stop, line) : { path, line };
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

  const showCursorAt = useCallback((stop: ReviewStop, line?: number, scroll: ScrollLogicalPosition = 'start') => {
    scrolledKeyRef.current = null;
    scrollModeRef.current = scroll;
    setCursorKey(stopCursorKey(stop, line));
  }, []);

  const selectMessage = useCallback((index: number, list = messageStops, line?: number) => {
    const at = list[index];
    setMapPlace(mapPlaceOf(bundle, list, index));
    const stop = at ? bundle.stops.find((candidate) => candidate.heading_line === at.stopLine) : undefined;
    if (stop) showCursorAt(stop, line);
  }, [bundle, messageStops, showCursorAt]);

  const pickMessage = useCallback((message: ReviewFlowMessage) => {
    const index = messageStops.findIndex((at) => at.message === message);
    if (index >= 0) selectMessage(index);
  }, [messageStops, selectMessage]);

  const switchSide = useCallback((next: Side) => {
    if (!sides || next === side) return;
    const list = mapStops(mapFlows(bundle, next));
    const current = messageStops[mapIndex];
    const same = current ? list.findIndex((at) => at.stopLine === current.stopLine) : -1;
    setSide(next);
    selectMessage(Math.max(0, same), list);
  }, [bundle, mapIndex, messageStops, selectMessage, side, sides]);

  /** The stop and line the cursor stands on in the current frame. */
  const place = useCallback((): { stop?: ReviewStop; line?: number } => {
    if (frame === 'walkthrough') return { stop: stageStop, line: stageCursorLine };
    const element = currentTarget();
    const stop = stopOf(element);
    if (stop) return { stop, line: Number(element?.getAttribute('data-line')) || undefined };
    if (frame === 'map') {
      const at = messageStops[mapIndex];
      return { stop: bundle.stops.find((candidate) => candidate.heading_line === at?.stopLine) };
    }
    const following = Array.from(docRef.current?.querySelectorAll<HTMLElement>('[data-review-stop-head]') ?? [])
      .find((head) => element !== null && (element.compareDocumentPosition(head) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0);
    return { stop: stopOf(following ?? null) };
  }, [bundle, currentTarget, frame, mapIndex, messageStops, stageCursorLine, stageStop, stopOf]);

  const switchFrame = useCallback((next: Frame) => {
    if (next === frame || !offered.includes(next)) return;
    const { stop, line } = place();
    setFiles([]);
    setFilled(false);
    setSearch(null);
    setChosenFrame(next);
    if (next === 'brief') {
      if (stop) showCursorAt(stop, line, 'center');
    } else if (next === 'walkthrough') {
      const target = stop ?? bundle.stops[0];
      if (target) showStage(target, stop ? line : undefined);
    } else {
      const forSide = (value: Side) => mapStops(mapFlows(bundle, value));
      const otherSide: Side = side === 'after' ? 'before' : 'after';
      const index = stop ? forSide(side).findIndex((at) => at.stopLine === stop.heading_line) : -1;
      const other = stop && index < 0 && sides ? forSide(otherSide).findIndex((at) => at.stopLine === stop.heading_line) : -1;
      if (other >= 0) {
        setSide(otherSide);
        selectMessage(other, forSide(otherSide), line);
      } else {
        selectMessage(Math.max(0, index), forSide(side), index >= 0 ? line : undefined);
      }
    }
  }, [bundle, frame, offered, place, selectMessage, showCursorAt, showStage, side, sides]);

  const stepStage = useCallback((delta: number) => {
    if (!stageStop) return;
    const index = bundle.stops.indexOf(stageStop);
    const next = bundle.stops[Math.max(0, Math.min(bundle.stops.length - 1, index + delta))];
    if (next && next !== stageStop) showStage(next);
  }, [bundle, showStage, stageStop]);

  const openedRef = useRef(false);
  useEffect(() => {
    if (openedRef.current) return;
    openedRef.current = true;
    if (frame === 'walkthrough' && bundle.stops[0]) showStage(bundle.stops[0]);
    if (frame === 'map') selectMessage(0);
  }, [bundle, frame, selectMessage, showStage]);

  const shownBundleRef = useRef(bundle);
  useEffect(() => {
    if (shownBundleRef.current === bundle) return;
    shownBundleRef.current = bundle;
    const stage = frame === 'walkthrough' ? stopOfStage(bundle, stageKey) : undefined;
    filesRef.current.forEach((file, index) => {
      if (index === 0 && stage) showStage(stage, undefined, true);
      else loadFile(file.id, file.target.path, rowIndexAfterRefresh);
    });
  }, [bundle, frame, loadFile, showStage, stageKey]);

  useEffect(() => {
    if (!stageStop) return;
    docRef.current?.querySelector(`[data-review-stop-head="${stageStop.heading_line}"]`)?.scrollIntoView?.({ block: 'nearest' });
  }, [stageStop]);

  const docLineFor = useCallback((path: string, line: number): number => {
    const stops = bundle.stops.filter((stop) => stop.path === path);
    const holding = stops.find((stop) => stop.now_from && line >= stop.now_from && line <= stop.now_to);
    return (holding ?? stops[0])?.heading_line ?? content.split('\n').length + 1;
  }, [bundle, content]);

  const markTarget = useCallback((): MarkTarget | null => {
    if (openFileView) {
      const rows = openFileView.rows;
      if (!rows || rows.length === 0) return null;
      const from = Math.min(openFileView.selectFrom ?? openFileView.cursor, openFileView.cursor);
      const to = Math.max(openFileView.selectFrom ?? openFileView.cursor, openFileView.cursor);
      const path = openFileView.target.path;
      const anchor = fileRowsAnchor(path, rows.slice(from, to + 1), rows.slice(to + 1), (line) => docLineFor(path, line));
      return anchor ? { kind: 'file', anchor } : null;
    }
    const element = currentTarget();
    if (!element) return null;
    if (!element.hasAttribute('data-review-cursor')) {
      const blockId = element.getAttribute('data-block-id');
      return blockId ? { kind: 'block', blockId, quote: element.textContent ?? '' } : null;
    }
    const picked = selectedRows(element);
    const path = element.getAttribute('data-path');
    const stop = stopOf(element);
    if (picked.length === 0 || !path || !stop) return null;
    const targets = documentTargets(scope());
    const following = targets.slice(targets.indexOf(picked[picked.length - 1]) + 1)
      .filter((target) => target.getAttribute('data-review-cursor') === 'row' && stopOf(target) === stop);
    const anchor = fileRowsAnchor(path, picked.map(rowOf), following.map(rowOf), () => stop.heading_line);
    return anchor ? { kind: 'file', anchor } : null;
  }, [currentTarget, docLineFor, openFileView, scope, selectedRows, stopOf]);

  const openComposer = useCallback((kind: MarkKind) => {
    const target = markTarget();
    if (target && marks) setComposer({ kind, target });
  }, [markTarget, marks]);

  const saveMark = useCallback((text: string) => {
    if (!composer || !marks) return;
    const { kind, target } = composer;
    const mark = target.kind === 'file'
      ? marks.addFileMark(kind, text, target.anchor)
      : marks.addBlockMark(kind, text, target.blockId);
    setComposer(null);
    setSelectFrom(null);
    if (openFileView?.selectFrom !== undefined) moveFileCursor((file) => file.cursor);
    rootRef.current?.focus({ preventScroll: true });
    if (!mark || kind !== 'question') return;
    setAsking((current) => new Map(current).set(mark.id, 'Asking…'));
    void marks.ask(mark.id).then((problem) => {
      setAsking((current) => new Map(current).set(mark.id, problem ? `Not asked: ${problem}` : 'Asked — waiting for the answer'));
    });
  }, [composer, marks, moveFileCursor, openFileView, rootRef]);

  const cancelComposer = useCallback(() => {
    setComposer(null);
    rootRef.current?.focus({ preventScroll: true });
  }, [rootRef]);

  const openSend = useCallback(() => {
    if (!marks) return;
    setComposer(null);
    setSending({ verdict: annotations.length > 0 ? 'request_changes' : 'approve' });
  }, [annotations.length, marks]);

  useImperativeHandle(handleRef, () => ({ openSend }), [openSend]);

  const sendReview = useCallback(() => {
    if (!sending || !marks || marks.destination === null) return;
    marks.send(sending.verdict);
    setSending(null);
  }, [marks, sending]);

  const searchOpen = search !== null;
  const searchWasOpenRef = useRef(false);
  useEffect(() => {
    if (searchWasOpenRef.current && !searchOpen) rootRef.current?.focus({ preventScroll: true });
    searchWasOpenRef.current = searchOpen;
  }, [rootRef, searchOpen]);

  const focused = useFocusAround(rootRef, tileWindow);
  const selecting = selectFrom !== null || openFileView?.selectFrom !== undefined;
  const escapes = !focused ? null
    : composer ? 'composer'
      : sending ? 'send'
        : searchOpen ? 'search'
          : help ? 'help'
            : selecting ? 'selection'
              : frame === 'walkthrough' ? (files.length > 1 ? 'stage' : null)
                : openFileView ? 'file' : null;
  useEscapeStack(cancelComposer, escapes === 'composer');
  useEscapeStack(() => setSending(null), escapes === 'send');
  useEscapeStack(() => setSearch(null), escapes === 'search');
  useEscapeStack(() => setHelp(false), escapes === 'help');
  useEscapeStack(() => {
    setSelectFrom(null);
    if (openFileView?.selectFrom !== undefined) moveFileCursor((file) => file.cursor);
  }, escapes === 'selection');
  useEscapeStack(backToStage, escapes === 'stage');
  useEscapeStack(closeFileView, escapes === 'file');

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
        const canGoBack = frame === 'walkthrough' ? files.length > 1 : openFileView !== null;
        if (canGoBack) back();
        return canGoBack;
      }
      if (event.ctrlKey || event.metaKey || event.altKey) return false;
      if (help) {
        if (event.key === '?') setHelp(false);
        return event.key === '?';
      }
      if (sending) {
        const verdict = verdictForKey(event.key);
        if (verdict) setSending({ verdict });
        if (event.key === 'Enter') sendReview();
        return true;
      }
      const frameKey = FRAME_KEYS.find(([key]) => key === event.key);
      if (frameKey) {
        switchFrame(frameKey[1]);
        return true;
      }
      if (marks && (event.key === 'c' || event.key === 'a')) {
        openComposer(event.key === 'a' ? 'question' : 'comment');
        return true;
      }
      const fileRows = openFileView?.rows;
      if (frame === 'walkthrough' || openFileView) {
        const stage = frame === 'walkthrough';
        switch (event.key) {
          case 'ArrowDown': moveFileCursor((file) => file.cursor + 1, event.shiftKey); return true;
          case 'ArrowUp': moveFileCursor((file) => file.cursor - 1, event.shiftKey); return true;
          case 'PageDown': moveFileCursor((file) => file.cursor + PAGE_ROWS); return true;
          case 'PageUp': moveFileCursor((file) => file.cursor - PAGE_ROWS); return true;
          case 'ArrowRight':
            if (stage) stepStage(1);
            else if (fileRows) moveFileCursor((file) => changeStarts(fileRows).find((start) => start > file.cursor) ?? file.cursor);
            return true;
          case 'ArrowLeft':
            if (stage) stepStage(-1);
            else if (fileRows) moveFileCursor((file) => [...changeStarts(fileRows)].reverse().find((start) => start < file.cursor) ?? file.cursor);
            return true;
          case 'z': setFilled((value) => !value); return true;
        }
      } else {
        switch (event.key) {
          case 'ArrowDown': moveLine(1, event.shiftKey); return true;
          case 'ArrowUp': moveLine(-1, event.shiftKey); return true;
          case 'ArrowRight': if (frame === 'map') selectMessage(Math.min(messageStops.length - 1, mapIndex + 1)); else moveStop(1); return true;
          case 'ArrowLeft': if (frame === 'map') selectMessage(Math.max(0, mapIndex - 1)); else moveStop(-1); return true;
          case 'b': if (frame !== 'map' || !sides) return false; switchSide(side === 'after' ? 'before' : 'after'); return true;
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
  }, [back, expandAtCursor, files.length, frame, help, mapIndex, marks, messageStops.length, moveFileCursor, moveLine, moveStop, openAtCursor, openComposer, openFileView, openSearch, rootRef, selectMessage, sendReview, sending, side, sides, stepStage, switchFrame, switchSide, tileWindow]);

  const onScopeClick = useCallback((event: React.MouseEvent) => {
    const target = event.target as Element;
    const element = target.closest<HTMLElement>('[data-review-cursor], [data-block-id]');
    if (element && scope()?.contains(element)) setCursor(element);
  }, [scope, setCursor]);

  const searchRepository = useCallback((query: string) => {
    if (!sendReviewSearch) return Promise.reject(new Error('Not connected to the daemon'));
    return sendReviewSearch(host, document, query);
  }, [document, host, sendReviewSearch]);

  const stage = useMemo(
    () => (stageStop ? { stopLine: stageStop.heading_line, line: stageCursorLine } : null),
    [stageCursorLine, stageStop],
  );
  const removeMark = marks?.deleteAnnotation;
  const context = useMemo<ReviewReaderContextValue>(
    () => ({ bundle, expansions, frame, stage, showStop: showStage, openFile, openSearch, marksByLine, asking, removeMark }),
    [asking, bundle, expansions, frame, marksByLine, stage, showStage, openFile, openSearch, removeMark],
  );

  return (
    <ReviewReaderContext.Provider value={context}>
      <div className={`review-frame review-frame--${frame}${frame === 'walkthrough' && filled ? ' review-frame--script-hidden' : ''}`}>
        <div className="review-doc" ref={docRef} onClick={onScopeClick} hidden={frame === 'map'}>
          {children}
        </div>
        {frame === 'walkthrough' && openFileView ? (
          <FileView
            file={openFileView}
            filled={filled}
            stage
            canGoBack={files.length > 1}
            onBack={back}
            onPick={(index) => moveFileCursor(() => index)}
          />
        ) : null}
        {frame === 'map' ? (
          <MapFrame
            bundle={bundle}
            flows={flows}
            stops={messageStops}
            at={mapIndex}
            side={side}
            sides={sides}
            detailRef={detailRef}
            onPick={pickMessage}
            onSide={switchSide}
            onDetailClick={onScopeClick}
          />
        ) : null}
      </div>
      {frame !== 'walkthrough' && openFileView ? (
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
      {composer && marks ? (
        <MarkComposer kind={composer.kind} target={composer.target} destination={marks.destination} onSave={saveMark} />
      ) : null}
      {sending && marks ? (
        <SendSheet marks={annotations} verdict={sending.verdict} destination={marks.destination} onPick={(verdict) => setSending({ verdict })} onSend={sendReview} />
      ) : null}
      <ReviewKeyLine frame={frame} offered={offered} fileOpen={frame !== 'walkthrough' && openFileView !== null} sides={sides} marking={marks !== undefined} onFrame={switchFrame} />
    </ReviewReaderContext.Provider>
  );
}
