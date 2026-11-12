import { useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode, Ref, RefObject } from 'react';
import type { CodeNavLocation, ReviewBundle, ReviewFileResultMessage, ReviewFlowMessage, ReviewRow, ReviewStop } from '../../types/generated';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { DaemonUnreachable } from '../../hooks/useDaemonSocket';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useTileWindow } from '../../tileWindow';
import { isDomInstance } from '../../utils/realm';
import { rowKey } from './CodeRows';
import { changeStarts, FileView, rowIndexAfterRefresh, rowIndexOf, type OpenFile } from './FileView';
import { FRAME_KEYS, hasSides, mapFlows, mapIndexOf, mapPlaceOf, mapStops, offeredFrames, openingFrame, type Frame, type MapPlace, type Side } from './frames';
import { MAP_COLUMNS, MapFrame, mapFitsBeside } from './MapFrame';
import { isOwnDefinition, labelNames, LATE_ANSWER_MS, namesOn, OFFER_MS, offerText, type LabeledName, type NavAnswer, type NavKind, type NavLine, type Picking } from './navigation';
import { NavPanel } from './NavPanel';
import { languageToSetUp, ReviewKeyLine, ReviewKeySheet, type ReviewSetup } from './ReviewKeys';
import { CURSOR_CLASS, expansionKey, ReviewReaderContext, type FileTarget, type ReviewReaderContextValue, type RowLabels, type StopExpansion } from './reviewContext';
import { SearchPanel } from './SearchPanel';
import { TokenCache } from './tokenize';
import { stopTarget } from './StopBlock';
import { fileAnchorPlace, type Annotation } from '../MarkdownReader/annotations/types';
import { fileRowsAnchor, MarkComposer, MarkRow, marksByLine as marksAtLines, placeFileMark, SendSheet, verdictForKey, type MarkKind, type MarkPlace, type MarkTarget, type ReviewMarks, type ReviewVerdict } from './ReviewMarks';
import { findEditUnit } from './editUnit';
import { docBlocks } from './docBlocks';
import { showLeadIn } from './leadIn';
import { blocksOutsideStep, isOutsideStep, OUTSIDE_STEP } from './mapStep';
import { isDocumentPath } from './DocStop';
import { EditBox, editStart, editText, fileLines, placeEdit, whereEditStands, type OpenEdit } from './ReviewEdits';
import './ReviewReader.css';

const MORE_LINES = 10;
const OPEN_BOX = 'Save or cancel the open box first: Enter saves, Escape cancels.';
const REMOVED_LINE = 'This line was removed. i edits the code as it is now.';

interface EditPlace {
  path: string;
  line: number;
  selection?: [number, number];
  block?: [number, number];
  docLine: number;
}
const SELECTED_CLASS = 'review-selected';
const NO_MARKS: ReviewMarks['annotations'] = [];

export interface ReviewReaderHandle {
  /** Shows what a send carries and takes the verdict. */
  openSend(): void;
  /** The file marks whose code is no longer where they were made. */
  movedMarkIds(): string[];
}
const PAGE_ROWS = 20;
const NAV_KEYS: Record<string, NavKind> = { f: 'definition', r: 'references', h: 'hover' };

function askingText(kind: NavKind, name: string): string {
  switch (kind) {
    case 'definition': return `Finding the definition of ${name}…`;
    case 'references': return `Finding the uses of ${name}…`;
    case 'hover': return `Reading the type of ${name}…`;
  }
}

function emptyText(kind: NavKind, name: string): string {
  switch (kind) {
    case 'definition': return `The language server knows no definition of ${name}.`;
    case 'references': return `The language server knows no uses of ${name}.`;
    case 'hover': return `The language server has no type for ${name}.`;
  }
}

const UNREACHABLE = 'The daemon is not reachable, so nothing was asked. Ask again once it is back.';

function pickingText(kind: NavKind): string {
  switch (kind) {
    case 'definition': return 'Go to the definition of which name? Type its label.';
    case 'references': return 'List the uses of which name? Type its label.';
    case 'hover': return 'Show the type of which name? Type its label.';
  }
}

function OfferLine({ answer }: { answer: NavAnswer }) {
  const { text, take } = offerText(answer);
  return (
    <div className="review-nav-note review-nav-offer" role="status" aria-label="Offered answer">
      {text} · <kbd>↵</kbd> {take} · <kbd>esc</kbd>
    </div>
  );
}

/** A line above the keys: a question on its way, or why one got no answer. */
interface NavNote {
  text: string;
  tone: 'asking' | 'status' | 'alert';
}

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

/** Whether the Map's frame is wide enough for the diagram beside the step,
 * kept current as the frame resizes; false outside the Map. */
function useMapBeside(ref: RefObject<HTMLElement | null>, tileWindow: Window, bundle: ReviewBundle, map: boolean): boolean {
  const [beside, setBeside] = useState(false);
  useLayoutEffect(() => {
    const element = ref.current;
    if (!map || !element) return undefined;
    const measure = () => setBeside(mapFitsBeside(bundle, element.getBoundingClientRect().width));
    measure();
    const observer = new tileWindow.ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [bundle, map, ref, tileWindow]);
  return map && beside;
}

function cursorKeyOf(element: Element): string {
  const key = element.getAttribute('data-cursor-key');
  return key ?? `block:${element.getAttribute('data-block-id')}`;
}

function stopCursorKey(stop: ReviewStop, line?: number): string | null {
  if (isDocumentPath(stop.path)) {
    const { blocks } = docBlocks(stop.rows);
    const block = (line === undefined ? undefined : blocks.find((candidate) => line >= candidate.from && line <= candidate.to)) ?? blocks[0];
    return block ? `stop:${stop.path}:block:${block.from}` : null;
  }
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

type CursorScroll = ScrollLogicalPosition | 'lead-in';

type StageChange = 'step' | 'pick' | 'refresh';

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
    (element.hasAttribute('data-review-cursor') || element.querySelector('[data-block-id]') === null) && !isOutsideStep(element)
  ));
}

function findTarget(doc: HTMLElement | null, key: string | null): HTMLElement | null {
  if (!doc || !key) return null;
  const element = key.startsWith('block:')
    ? doc.querySelector<HTMLElement>(`[data-block-id="${CSS.escape(key.slice(6))}"]`)
    : doc.querySelector<HTMLElement>(`[data-cursor-key="${CSS.escape(key)}"]`);
  return element && !isOutsideStep(element) ? element : null;
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
  return { kind: element.getAttribute('data-kind') ?? ' ', old: number('data-old'), now: number('data-line'), head: number('data-head'), text: rowText(element) };
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

/** What a review shows, apart from the language servers' states, which change on their own. */
function reviewContentKey(bundle: ReviewBundle): string {
  return JSON.stringify({ ...bundle, navigation: undefined });
}

/** The delivered bundle, kept as the same object while only navigation changes. */
function useReviewContent(delivered: ReviewBundle): ReviewBundle {
  const key = useMemo(() => reviewContentKey(delivered), [delivered]);
  const shownRef = useRef({ key, bundle: delivered });
  if (shownRef.current.key !== key) shownRef.current = { key, bundle: delivered };
  return shownRef.current.bundle;
}

function firstChangedLine(was: string, now: string): number {
  const before = was.split('\n');
  const after = now.split('\n');
  let line = 0;
  while (line < after.length - 1 && before[line] === after[line]) line += 1;
  return line;
}

function sameStop(a: ReviewStop | undefined, b: ReviewStop | undefined): boolean {
  return a !== undefined && b !== undefined && JSON.stringify(a) === JSON.stringify(b);
}

export function ReviewReader({
  bundle: delivered,
  host,
  document,
  content,
  rootRef,
  marks,
  handleRef,
  openInEditor,
  setup,
  children,
}: {
  bundle: ReviewBundle;
  host?: string;
  document: string;
  content: string;
  rootRef: RefObject<HTMLDivElement | null>;
  marks?: ReviewMarks;
  handleRef?: Ref<ReviewReaderHandle>;
  openInEditor?: (root: string, path: string) => void;
  setup?: ReviewSetup;
  children: ReactNode;
}) {
  const bundle = useReviewContent(delivered);
  const daemon = useOptionalDaemonApi();
  const sendReviewFile = daemon?.sendReviewFile;
  const sendReviewSearch = daemon?.sendReviewSearch;
  const sendReviewEdit = daemon?.sendReviewEdit;
  const openDocumentInEditor = useMemo(
    () => (openInEditor && bundle.repository ? (path: string) => openInEditor(bundle.repository, path) : undefined),
    [bundle.repository, openInEditor],
  );
  const sendCodeNav = daemon?.sendCodeNav;
  const tileWindow = useTileWindow();
  const docRef = useRef<HTMLDivElement | null>(null);
  const frameRef = useRef<HTMLDivElement | null>(null);
  const fileBodyRef = useRef<HTMLDivElement | null>(null);
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
  const [expansions, setExpansions] = useState<ReadonlyMap<string, StopExpansion>>(new Map());
  const briefEditedRef = useRef<{ path: string; line: number } | null>(null);
  const expansionsRef = useRef(expansions);
  expansionsRef.current = expansions;
  const [markFiles, setMarkFiles] = useState<ReadonlyMap<string, readonly ReviewRow[]>>(new Map());
  const [files, setFiles] = useState<OpenFile[]>([]);
  const [filled, setFilled] = useState(false);
  const [search, setSearch] = useState<{ query: string } | null>(null);
  const [help, setHelp] = useState(false);
  const [filesShown, setFilesShown] = useState(false);
  const toggleFiles = useCallback(() => setFilesShown((shown) => !shown), []);
  const [selectFrom, setSelectFrom] = useState<string | null>(null);
  const [composer, setComposer] = useState<{ kind: MarkKind; target: MarkTarget } | null>(null);
  const [sending, setSending] = useState<{ verdict: ReviewVerdict } | null>(null);
  const [asking, setAsking] = useState<ReadonlyMap<string, string>>(new Map());
  const [editing, setEditing] = useState<OpenEdit | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [picking, setPicking] = useState<Picking | null>(null);
  const [navPanel, setNavPanel] = useState<NavAnswer | null>(null);
  const [navNote, setNavNote] = useState<NavNote | null>(null);
  const [offer, setOffer] = useState<NavAnswer | null>(null);
  const [asked, setAsked] = useState<NavLine['at'] | null>(null);
  const overlay = editing ? 'edit' : composer ? 'composer' : sending ? 'send' : search ? 'search' : help ? 'help' : '';
  const overlayRef = useRef(overlay);
  overlayRef.current = overlay;
  const shownOffer = overlay === '' ? offer : null;
  const questionRef = useRef(0);
  const positionRef = useRef('');
  const selectedRef = useRef<HTMLElement[]>([]);
  const annotations = marks?.annotations ?? NO_MARKS;
  const cursorElementRef = useRef<HTMLElement | null>(null);
  const cursorIndexRef = useRef(0);
  const scrolledKeyRef = useRef<string | null>(null);
  const scrollModeRef = useRef<CursorScroll>('nearest');
  const stageScrollRef = useRef<StageChange>('refresh');
  const fileReadsRef = useRef(new Map<string, Promise<ReviewFileResultMessage>>());
  const fileIdRef = useRef(0);
  const readsRef = useRef(new Map<number, number>());
  const openFileView = files.length > 0 ? files[files.length - 1] : null;
  const filesRef = useRef(files);
  filesRef.current = files;
  const onStage = frame === 'walkthrough' && files.length === 1 && openFileView !== null;
  const stageCursorLine = onStage ? openFileView.rows?.[openFileView.cursor]?.now : undefined;
  positionRef.current = `${overlay}|${frame}|${cursorKey ?? ''}|${files.length}|${openFileView ? `${openFileView.target.path}:${openFileView.cursor}` : ''}`;

  const docLineFor = useCallback((path: string, line: number): number => {
    const stops = bundle.stops.filter((stop) => stop.path === path);
    const holding = stops.find((stop) => line >= stop.from && line <= stop.to);
    return (holding ?? stops[0])?.heading_line ?? content.split('\n').length + 1;
  }, [bundle, content]);

  const markPlaces = useMemo(() => {
    const places = new Map<string, MarkPlace>();
    for (const annotation of annotations) {
      const anchor = annotation.fileAnchor;
      if (!anchor) continue;
      const whole = markFiles.get(anchor.path);
      if (annotation.type === 'edit') {
        const place = !annotation.edit ? null : placeEdit(annotation.edit, whole ?? bundle.stops.filter((stop) => stop.path === anchor.path).flatMap((stop) => stop.rows), whole !== undefined);
        if (place !== null) places.set(annotation.id, place);
        continue;
      }
      const lists = whole
        ? [{ rows: whole, covers: () => true }]
        : bundle.stops.filter((stop) => stop.path === anchor.path)
          .map((stop) => ({ rows: stop.rows, covers: (line: number) => line >= stop.from && line <= stop.to }));
      const place = placeFileMark(anchor, lists);
      if (place !== null) places.set(annotation.id, place);
    }
    return places;
  }, [annotations, bundle, markFiles]);
  const marksByLine = useMemo(() => marksAtLines(annotations, markPlaces), [annotations, markPlaces]);
  const movedMarks = useMemo(() => annotations.filter((annotation) => markPlaces.get(annotation.id) === 'moved'), [annotations, markPlaces]);

  const scope = useCallback((): HTMLElement | null => (frame === 'walkthrough' ? null : docRef.current), [frame]);

  const readFile = useCallback((path: string): Promise<ReviewFileResultMessage> => {
    if (!sendReviewFile) return Promise.reject(new Error('Not connected to the daemon'));
    return sendReviewFile(host, document, path);
  }, [document, host, sendReviewFile]);

  /** The file as this bundle found it, read once for every use but the file view. */
  const readOnce = useCallback((path: string): Promise<ReviewFileResultMessage> => {
    let read = fileReadsRef.current.get(path);
    if (!read) {
      read = readFile(path);
      read.catch(() => fileReadsRef.current.delete(path));
      fileReadsRef.current.set(path, read);
    }
    return read;
  }, [readFile]);

  const wholeFile = useCallback((path: string): Promise<ReviewRow[]> => readOnce(path).then((answer) => answer.rows ?? []), [readOnce]);

  const readBundleRef = useRef(bundle);
  useEffect(() => {
    const was = readBundleRef.current;
    if (was === bundle) return;
    readBundleRef.current = bundle;
    fileReadsRef.current.clear();
    setMarkFiles(new Map());
    const stopAt = (stops: readonly ReviewStop[], key: string) => stops.find((stop) => expansionKey(stop) === key);
    const changed = [...expansionsRef.current].flatMap(([key, expansion]) => {
      const stop = stopAt(bundle.stops, key);
      return stop && !sameStop(stopAt(was.stops, key), stop) ? [{ key, stop, extraLines: expansion.extraLines }] : [];
    });
    setExpansions((current) => new Map([...current].filter(([key]) => stopAt(bundle.stops, key) !== undefined)));
    changed.forEach(({ key, stop, extraLines }) => {
      void wholeFile(stop.path).then((file) => {
        if (readBundleRef.current !== bundle) return;
        const rows = widen(stop.rows, file, extraLines);
        setExpansions((current) => {
          const next = new Map(current);
          if (rows) next.set(key, { rows, extraLines });
          else next.delete(key);
          return next;
        });
      }, () => setExpansions((current) => {
        const next = new Map(current);
        next.delete(key);
        return next;
      }));
    });
  }, [bundle, wholeFile]);

  const tokenCache = useMemo(() => new TokenCache(), [bundle.repository, bundle.base]);

  const stopFiles = useMemo(
    () => [...new Map(bundle.stops.map((stop) => [stop.path, stop.file_hash])).entries()].map(([path, hash]) => `${path}\0${hash}`).join('\n'),
    [bundle.stops],
  );
  useEffect(() => {
    let current = true;
    void (async () => {
      for (const [path, hash] of stopFiles.split('\n').filter(Boolean).map((entry) => entry.split('\0'))) {
        if (!current) return;
        if (tokenCache.has(path, hash)) continue;
        const answer = await readOnce(path).catch(() => null);
        if (!current) return;
        if (!answer?.rows || answer.hash === undefined) continue;
        if (!tokenCache.fits(answer.rows.length)) return;
        void tokenCache.warm(path, answer.hash, answer.rows.map((row) => row.text));
      }
    })();
    return () => { current = false; };
  }, [readOnce, stopFiles, tokenCache]);

  /** Reads a file into the stack entry `id`. A newer read of the same entry
   * replaces an older one. The entry's edited line, when it has one, places the
   * cursor; otherwise `place` picks the row from the entry as it is when the
   * rows arrive. */
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
        settle((open) => {
          const edited = open.edited === undefined ? -1 : rows.findIndex((row) => row.now === open.edited);
          return { rows, hash: answer.hash, problem: answer.problem, error: undefined, cursor: edited >= 0 ? edited : place(rows, open), edited: undefined };
        });
      },
      (error: unknown) => settle(() => ({ rows: null, error: error instanceof Error ? error.message : String(error) })),
    );
  }, [readFile]);

  /** Reads a file the reader wrote again, for the marks and every open view of it. */
  const refreshFile = useCallback((path: string) => {
    fileReadsRef.current.delete(path);
    void wholeFile(path).then((rows) => setMarkFiles((known) => new Map(known).set(path, rows)), () => undefined);
    filesRef.current.forEach((file) => {
      if (file.target.path === path) loadFile(file.id, path, rowIndexAfterRefresh);
    });
  }, [loadFile, wholeFile]);

  const markedPaths = useMemo(
    () => [...new Set(annotations.flatMap((annotation) => (annotation.fileAnchor ? [annotation.fileAnchor.path] : [])))].sort().join('\n'),
    [annotations],
  );
  useEffect(() => {
    if (!markedPaths) return;
    let current = true;
    for (const path of markedPaths.split('\n')) {
      void wholeFile(path).then(
        (rows) => { if (current) setMarkFiles((known) => (known.get(path) === rows ? known : new Map(known).set(path, rows))); },
        () => undefined,
      );
    }
    return () => { current = false; };
  }, [bundle, markedPaths, wholeFile]);

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
  const showStage = useCallback((stop: ReviewStop, line?: number, how: StageChange = 'step') => {
    const refresh = how === 'refresh';
    if (!refresh) setSearch(null);
    stageScrollRef.current = how;
    setStageKey(stageKeyOf(bundle, stop));
    const target = stopTarget(stop, line ?? stageLine(stop));
    const id = ++fileIdRef.current;
    setFiles((stack) => {
      const previous = stack[0]?.target.path === stop.path ? stack[0] : undefined;
      const stage = refresh && previous
        ? { ...previous, id, target }
        : { id, target, rows: previous?.rows ?? null, hash: previous?.hash, cursor: previous?.rows ? rowIndexOf(previous.rows, target.line) : 0 };
      return refresh ? [stage, ...stack.slice(1)] : [stage];
    });
    loadFile(id, stop.path, (rows, open) => (refresh ? rowIndexAfterRefresh(rows, open) : rowIndexOf(rows, target.line)));
  }, [bundle, loadFile]);

  const pickStage = useCallback((stop: ReviewStop) => showStage(stop, undefined, 'pick'), [showStage]);

  const returnToOpenBox = useCallback(() => {
    rootRef.current?.querySelector<HTMLTextAreaElement>('.review-composer textarea')?.focus({ preventScroll: true });
  }, [rootRef]);

  const openSearch = useCallback((query = '') => {
    if (editing || composer) {
      returnToOpenBox();
      setNotice(OPEN_BOX);
      return;
    }
    setSearch({ query });
  }, [composer, editing, returnToOpenBox]);

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

  const setCursor = useCallback((element: HTMLElement | null, scroll: CursorScroll = 'nearest') => {
    if (!element) return;
    briefEditedRef.current = null;
    scrollModeRef.current = scroll;
    setCursorKey(cursorKeyOf(element));
  }, []);

  const mapStopLine = frame === 'map' ? messageStops[mapIndex]?.stopLine ?? null : undefined;
  const drawnLines = useMemo(() => new Set(bundle.flows.filter((flow) => flow.drawn).map((flow) => flow.line)), [bundle]);
  useLayoutEffect(() => {
    const card = docRef.current?.querySelector('.md-reader-card');
    if (mapStopLine === undefined || !card) return undefined;
    const outside = blocksOutsideStep(card, mapStopLine, drawnLines);
    for (const block of outside) block.setAttribute(OUTSIDE_STEP, '');
    return () => {
      for (const block of outside) block.removeAttribute(OUTSIDE_STEP);
    };
  }, [content, drawnLines, mapStopLine]);

  useLayoutEffect(() => {
    cursorElementRef.current?.classList.remove(CURSOR_CLASS);
    cursorElementRef.current = null;
    const root = scope();
    if (cursorKey === null || root === null) return;
    const edit = briefEditedRef.current;
    if (edit) {
      const edited = documentTargets(root).find((target) => target.getAttribute('data-kind') === '+'
        && target.getAttribute('data-path') === edit.path && target.getAttribute('data-line') === String(edit.line));
      if (edited) {
        briefEditedRef.current = null;
        if (cursorKeyOf(edited) !== cursorKey) {
          setCursorKey(cursorKeyOf(edited));
          return;
        }
      }
    }
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
      const scroll = scrollModeRef.current;
      if (scroll !== 'lead-in') element.scrollIntoView?.({ block: scroll });
      else {
        const stop = element.closest<HTMLElement>('[data-review-stop-head]');
        if (stop) showLeadIn(stop, root, element);
      }
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
    if (firstRow) setCursor(firstRow, 'lead-in');
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
    if (currentTarget()?.getAttribute('data-review-cursor') === 'facts') {
      toggleFiles();
      return;
    }
    const target = targetAtCursor();
    if (target && target !== 'search') openFile(target, { fill });
  }, [currentTarget, openFile, targetAtCursor, toggleFiles]);

  const expandAtCursor = useCallback(() => {
    const stop = stopOf(currentTarget());
    if (!stop) return;
    const key = expansionKey(stop);
    const shown = expansions.get(key);
    const extraLines = (shown?.extraLines ?? 0) + MORE_LINES;
    void wholeFile(stop.path).then(
      (file) => {
        const rows = widen(stop.rows, file, extraLines);
        if (rows) setExpansions((current) => new Map(current).set(key, { rows, extraLines }));
      },
      (error: unknown) => setExpansions((current) => new Map(current).set(key, { rows: shown?.rows ?? stop.rows, extraLines: shown?.extraLines ?? 0, error: error instanceof Error ? error.message : String(error) })),
    );
  }, [currentTarget, expansions, stopOf, wholeFile]);

  const showCursorAt = useCallback((stop: ReviewStop, line?: number, scroll: CursorScroll = 'start') => {
    scrolledKeyRef.current = null;
    scrollModeRef.current = scroll;
    setCursorKey(stopCursorKey(stop, line));
  }, []);

  const selectMessage = useCallback((index: number, list = messageStops, line?: number) => {
    const at = list[index];
    setMapPlace(mapPlaceOf(bundle, list, index));
    const stop = at ? bundle.stops.find((candidate) => candidate.heading_line === at.stopLine) : undefined;
    if (stop) showCursorAt(stop, line, 'lead-in');
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
    briefEditedRef.current = null;
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
    if (frame === 'walkthrough' && bundle.stops[0]) showStage(bundle.stops[0], undefined, 'pick');
    if (frame === 'map') selectMessage(0);
  }, [bundle, frame, selectMessage, showStage]);

  const shownBundleRef = useRef(bundle);
  useEffect(() => {
    if (shownBundleRef.current === bundle) return;
    shownBundleRef.current = bundle;
    const stage = frame === 'walkthrough' ? stopOfStage(bundle, stageKey) : undefined;
    filesRef.current.forEach((file, index) => {
      if (index === 0 && stage) showStage(stage, undefined, 'refresh');
      else loadFile(file.id, file.target.path, rowIndexAfterRefresh);
    });
  }, [bundle, frame, loadFile, showStage, stageKey]);

  useLayoutEffect(() => {
    const how = stageScrollRef.current;
    stageScrollRef.current = 'refresh';
    const doc = docRef.current;
    const card = stageStop && doc?.querySelector<HTMLElement>(`[data-review-stop-head="${stageStop.heading_line}"]`);
    if (!doc || !card || how === 'refresh') return;
    if (how === 'step') showLeadIn(card, doc, card);
    else card.scrollIntoView?.({ block: 'nearest' });
  }, [stageKey, stageStop]);

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
    if (element.getAttribute('data-review-cursor') === 'block') {
      const path = element.getAttribute('data-path');
      const from = Number(element.getAttribute('data-line'));
      const to = Number(element.getAttribute('data-line-to'));
      const stop = stopOf(element);
      if (!path || !stop || !(from > 0)) return null;
      const rows = expansions.get(expansionKey(stop))?.rows ?? stop.rows;
      const first = rows.findIndex((row) => row.now === from);
      const last = rows.findIndex((row) => row.now === Math.max(from, to));
      if (first < 0 || last < first) return null;
      const picked = rows.slice(first, last + 1).filter((row) => row.now !== undefined);
      const anchor = fileRowsAnchor(path, picked, rows.slice(last + 1), () => stop.heading_line);
      return anchor ? { kind: 'file', anchor } : null;
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
  }, [currentTarget, docLineFor, expansions, openFileView, scope, selectedRows, stopOf]);

  /** The file, line and selected lines the cursor stands on, for an edit, or why there is none. */
  const editPlace = useCallback((): EditPlace | string => {
    const span = (lines: number[]): [number, number] => [Math.min(...lines), Math.max(...lines)];
    if (openFileView) {
      const rows = openFileView.rows;
      if (!rows) return 'The file is still being read.';
      const line = rows[openFileView.cursor]?.now;
      if (line === undefined) return REMOVED_LINE;
      const from = Math.min(openFileView.selectFrom ?? openFileView.cursor, openFileView.cursor);
      const to = Math.max(openFileView.selectFrom ?? openFileView.cursor, openFileView.cursor);
      const lines = rows.slice(from, to + 1).map((row) => row.now).filter((now): now is number => now !== undefined);
      const path = openFileView.target.path;
      return { path, line, selection: openFileView.selectFrom !== undefined && lines.length > 0 ? span(lines) : undefined, docLine: docLineFor(path, line) };
    }
    const element = currentTarget();
    const kind = element?.getAttribute('data-review-cursor');
    const path = element?.getAttribute('data-path');
    if (!element || !path || (kind !== 'row' && kind !== 'block')) return 'Put the cursor on a line of code or a document stop to edit it.';
    const line = Number(element.getAttribute('data-line')) || undefined;
    if (line === undefined) return REMOVED_LINE;
    if (kind === 'block') {
      const to = Number(element.getAttribute('data-line-to')) || line;
      return { path, line, block: [line, to], docLine: stopOf(element)?.heading_line ?? docLineFor(path, line) };
    }
    const lines = selectedRows(element).map((row) => Number(row.getAttribute('data-line'))).filter((now) => now > 0);
    return { path, line, selection: selectFrom !== null && lines.length > 0 ? span(lines) : undefined, docLine: stopOf(element)?.heading_line ?? docLineFor(path, line) };
  }, [currentTarget, docLineFor, openFileView, selectFrom, selectedRows, stopOf]);

  const startEdit = useCallback(() => {
    const place = editPlace();
    if (!marks) return;
    if (typeof place === 'string') {
      setNotice(place);
      return;
    }
    setNotice(null);
    void readFile(place.path).then(async (answer) => {
      const { lines, heads } = fileLines(answer.rows);
      const selection = place.selection ? [place.selection[0] - 1, place.selection[1] - 1] as const : undefined;
      const unit = place.block
        ? { kind: 'block' as const, from: place.block[0] - 1, to: place.block[1] - 1 }
        : await findEditUnit(lines, place.path, place.line - 1, selection);
      setEditing({ path: place.path, hash: answer.hash ?? '', lines, heads, kind: unit.kind, from: unit.from, to: unit.to, docLine: place.docLine });
    }, (error: unknown) => setNotice(`Could not open the edit: ${error instanceof Error ? error.message : String(error)}`));
  }, [editPlace, marks, readFile]);

  const takeLine = useCallback((direction: -1 | 1) => {
    setEditing((edit) => {
      if (!edit) return edit;
      const from = direction < 0 ? Math.max(0, edit.from - 1) : edit.from;
      const to = direction > 0 ? Math.min(edit.lines.length - 1, edit.to + 1) : edit.to;
      if (from === edit.from && to === edit.to) return edit;
      return { ...edit, from, to, kind: edit.kind === 'block' ? 'block' : 'selection', taken: direction };
    });
  }, []);

  const closeEdit = useCallback(() => {
    setEditing(null);
    setSelectFrom(null);
    if (openFileView?.selectFrom !== undefined) moveFileCursor((file) => file.cursor);
    rootRef.current?.focus({ preventScroll: true });
  }, [moveFileCursor, openFileView, rootRef]);

  const saveEdit = useCallback((text: string) => {
    if (!editing || !marks || !sendReviewEdit) return;
    const was = editText(editing);
    if (text === was) {
      closeEdit();
      return;
    }
    setEditing({ ...editing, saving: true, problem: undefined });
    void sendReviewEdit(host, document, { path: editing.path, baseHash: editing.hash, from: editing.from + 1, to: editing.to + 1, text }).then((result) => {
      if (result.conflict) {
        setEditing({ ...editing, saving: false, problem: `${editing.path} changed since this edit opened, so nothing was written. Press esc, then i to edit it as it is now.` });
        return;
      }
      const to = editing.from + text.split('\n').length;
      marks.addEditMark({ path: editing.path, from: editing.from + 1, to, docLine: editing.docLine, quote: text }, { was, now: text, ...whereEditStands(editing.heads, editing.from, editing.to) });
      const edited = editing.from + 1 + firstChangedLine(was, text);
      if (openFileView) setFiles((stack) => stack.map((open, index) => (index === stack.length - 1 && open.target.path === editing.path ? { ...open, edited } : open)));
      else briefEditedRef.current = { path: editing.path, line: edited };
      closeEdit();
      refreshFile(editing.path);
    }, (error: unknown) => setEditing({ ...editing, saving: false, problem: error instanceof Error ? error.message : String(error) }));
  }, [closeEdit, document, editing, host, marks, openFileView, refreshFile, sendReviewEdit]);

  const undoEdit = useCallback((): boolean => {
    const found = editPlace();
    const place = typeof found === 'string' ? null : found;
    const covers = (annotation: Annotation) => {
      const last = markPlaces.get(annotation.id);
      if (typeof last !== 'number' || !place || annotation.fileAnchor?.path !== place.path) return false;
      return place.line <= last && place.line > last - (annotation.edit?.now.split('\n').length ?? 1);
    };
    const mark = place && [...annotations].reverse().find((annotation) => annotation.type === 'edit' && covers(annotation));
    if (!mark?.edit || !mark.fileAnchor || !marks || !sendReviewEdit) return false;
    const { path } = mark.fileAnchor;
    const edit = mark.edit;
    setNotice(null);
    void readFile(path).then(async (answer) => {
      const start = editStart(answer.rows ?? [], edit, true);
      if (start === 'moved' || start === null) {
        setNotice(`Your edit is no longer where you saved it in ${path}, so nothing was undone.`);
        return;
      }
      const result = await sendReviewEdit(host, document, { path, baseHash: answer.hash ?? '', from: start, to: start + edit.now.split('\n').length - 1, text: edit.was });
      if (result.conflict) {
        setNotice(`${path} changed while undoing, so nothing was written. Press u again.`);
        return;
      }
      marks.deleteAnnotation(mark.id);
      refreshFile(path);
    }).catch((error: unknown) => setNotice(`Could not undo the edit: ${error instanceof Error ? error.message : String(error)}`));
    return true;
  }, [annotations, document, editPlace, host, marks, markPlaces, readFile, refreshFile, sendReviewEdit]);

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
    if (editing || composer) {
      returnToOpenBox();
      setNotice(OPEN_BOX);
      return;
    }
    setSending({ verdict: annotations.length > 0 ? 'request_changes' : 'approve' });
  }, [annotations.length, composer, editing, marks, returnToOpenBox]);

  useImperativeHandle(handleRef, () => ({ openSend, movedMarkIds: () => movedMarks.map((mark) => mark.id) }), [movedMarks, openSend]);

  const sendReview = useCallback(() => {
    if (!sending || !marks || marks.destination === null) return;
    marks.send(sending.verdict);
    setSending(null);
  }, [marks, sending]);

  /** The line of code the cursor stands on, or why there is none to ask about. */
  const navLine = useCallback((): NavLine | string => {
    if (openFileView) {
      const row = openFileView.rows?.[openFileView.cursor];
      if (!row) return 'The file is still being read.';
      if (row.now === undefined) return 'This line was removed. Navigation reads the code as it is now.';
      return { path: openFileView.target.path, line: row.now, text: row.text, at: { fileId: openFileView.id, row: openFileView.cursor } };
    }
    const element = currentTarget();
    if (!element || element.getAttribute('data-review-cursor') !== 'row') return 'Put the cursor on a line of code to ask about its names.';
    const path = element.getAttribute('data-path');
    const line = Number(element.getAttribute('data-line'));
    if (!path || !(line > 0)) return 'This line was removed. Navigation reads the code as it is now.';
    return { path, line, text: rowText(element), at: { cursorKey: cursorKeyOf(element) } };
  }, [currentTarget, openFileView]);

  const follow = useCallback((location: CodeNavLocation) => {
    setNavPanel(null);
    openFile({ path: location.path, line: location.line, ...(location.outside ? { outside: true } : {}) });
  }, [openFile]);

  const takeOffer = useCallback((answer: NavAnswer) => {
    setOffer(null);
    if (answer.kind === 'definition' && answer.locations.length === 1) follow(answer.locations[0]);
    else setNavPanel({ ...answer, cursor: 0 });
  }, [follow]);

  useEffect(() => {
    if (!shownOffer) return undefined;
    const expire = setTimeout(() => setOffer((current) => (current === shownOffer ? null : current)), OFFER_MS);
    return () => clearTimeout(expire);
  }, [shownOffer]);

  const askRef = useRef<(kind: NavKind, line: NavLine, name: LabeledName) => void>(() => undefined);
  const ask = useCallback((kind: NavKind, line: NavLine, name: LabeledName) => {
    setPicking(null);
    setNavPanel(null);
    setOffer(null);
    if (!sendCodeNav) {
      setNavNote({ text: UNREACHABLE, tone: 'alert' });
      return;
    }
    const question = ++questionRef.current;
    const askedAt = Date.now();
    const askedFrom = positionRef.current;
    setNavNote({ text: askingText(kind, name.name), tone: 'asking' });
    setAsked(line.at);
    void sendCodeNav(host, document, { path: line.path, line: line.line, character: name.character, kind }).then(
      (answer) => {
        if (questionRef.current !== question) return;
        setAsked(null);
        const locations = answer.locations ?? [];
        if (kind === 'definition' && locations.length === 1 && isOwnDefinition(locations[0], line.path, line.line, name.character)) {
          askRef.current('references', line, name);
          return;
        }
        if (locations.length === 0 && !answer.text) {
          setNavNote({ text: emptyText(kind, name.name), tone: 'status' });
          return;
        }
        setNavNote(null);
        const late = overlayRef.current !== '' || (Date.now() - askedAt > LATE_ANSWER_MS && positionRef.current !== askedFrom);
        if (kind === 'definition' && locations.length === 1 && !late) {
          follow(locations[0]);
          return;
        }
        const shown: NavAnswer = { kind, name: name.name, locations, text: answer.text, cursor: 0 };
        if (late) setOffer(shown);
        else setNavPanel(shown);
      },
      (error: unknown) => {
        if (questionRef.current !== question) return;
        setAsked(null);
        const text = error instanceof DaemonUnreachable ? UNREACHABLE : error instanceof Error ? error.message : String(error);
        setNavNote({ text, tone: 'alert' });
      },
    );
  }, [document, follow, host, sendCodeNav]);
  askRef.current = ask;

  const startSetup = useCallback((): boolean => {
    const language = languageToSetUp(delivered.navigation, setup);
    if (!setup || !language) return false;
    setNavNote({ text: `Starting an agent to set up ${language.language} navigation…`, tone: 'asking' });
    void setup.start(language).then((problem) => {
      setNavNote(problem ? { text: `Could not start setup: ${problem}`, tone: 'alert' } : null);
    });
    return true;
  }, [delivered.navigation, setup]);

  const toggleSetupOffer = useCallback((): boolean => {
    if (!setup || !delivered.navigation.some((entry) => entry.state === 'missing' || entry.state === 'failed')) return false;
    setup.setOffered(!setup.offered);
    setNavNote({ text: setup.offered ? 'Setup is no longer offered in this repository; s still sets up.' : 'Setup is offered again in this repository.', tone: 'status' });
    return true;
  }, [delivered.navigation, setup]);

  const startPicking = useCallback((kind: NavKind) => {
    setOffer(null);
    const line = navLine();
    if (typeof line === 'string') {
      setNavNote({ text: line, tone: 'status' });
      return;
    }
    const names = labelNames(namesOn(line.text));
    if (names.length === 0) {
      setNavNote({ text: 'This line has no names to ask about.', tone: 'status' });
      return;
    }
    if (names.length === 1) {
      ask(kind, line, names[0]);
      return;
    }
    setNavNote(null);
    setPicking({ kind, line, names, typed: '' });
  }, [ask, navLine]);

  const pickLabel = useCallback((key: string) => {
    if (!picking) return;
    const typed = picking.typed + key;
    const matching = picking.names.filter((name) => name.label.startsWith(typed));
    if (matching.length === 0) {
      setPicking(null);
      return;
    }
    const exact = matching.find((name) => name.label === typed);
    if (exact) ask(picking.kind, picking.line, exact);
    else setPicking({ ...picking, typed });
  }, [ask, picking]);

  const cancelQuestion = useCallback(() => {
    questionRef.current += 1;
    setNavNote(null);
    setAsked(null);
  }, []);

  const movePanelCursor = useCallback((delta: number) => {
    setNavPanel((panel) => (panel ? { ...panel, cursor: Math.max(0, Math.min(panel.locations.length - 1, panel.cursor + delta)) } : panel));
  }, []);

  const labels = useMemo<RowLabels | null>(
    () => (picking ? { at: picking.line.at, names: picking.names.filter((name) => name.label.startsWith(picking.typed)) } : null),
    [picking],
  );

  const searchOpen = search !== null;
  const searchWasOpenRef = useRef(false);
  useEffect(() => {
    if (searchWasOpenRef.current && !searchOpen) rootRef.current?.focus({ preventScroll: true });
    searchWasOpenRef.current = searchOpen;
  }, [rootRef, searchOpen]);

  const focused = useFocusAround(rootRef, tileWindow);
  const mapBeside = useMapBeside(frameRef, tileWindow, bundle, frame === 'map');
  const selecting = selectFrom !== null || openFileView?.selectFrom !== undefined;
  const escapes = !focused ? null
    : editing ? 'editor'
    : composer ? 'composer'
      : sending ? 'send'
        : searchOpen ? 'search'
          : help ? 'help'
            : picking ? 'picking'
              : navPanel ? 'answer'
                : shownOffer ? 'offer'
                : navNote ? 'note'
                  : selecting ? 'selection'
                    : frame === 'walkthrough' ? (files.length > 1 ? 'stage' : null)
                      : openFileView ? 'file' : null;
  useEscapeStack(closeEdit, escapes === 'editor');
  useEscapeStack(cancelComposer, escapes === 'composer');
  useEscapeStack(() => setSending(null), escapes === 'send');
  useEscapeStack(() => setSearch(null), escapes === 'search');
  useEscapeStack(() => setHelp(false), escapes === 'help');
  useEscapeStack(() => setPicking(null), escapes === 'picking');
  useEscapeStack(() => setNavPanel(null), escapes === 'answer');
  useEscapeStack(() => setOffer(null), escapes === 'offer');
  useEscapeStack(cancelQuestion, escapes === 'note');
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
      setNotice(null);
      const handled = handleKey(event);
      if (handled) event.preventDefault();
    };
    const handleKey = (event: KeyboardEvent): boolean => {
      if (editing || composer) {
        returnToOpenBox();
        return !event.metaKey;
      }
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
      if (picking) {
        if (event.key === 'Escape') return false;
        if (isPrintable(event)) pickLabel(event.key);
        else setPicking(null);
        return true;
      }
      if (navPanel) {
        switch (event.key) {
          case 'ArrowDown': movePanelCursor(1); return true;
          case 'ArrowUp': movePanelCursor(-1); return true;
          case 'PageDown': movePanelCursor(PAGE_ROWS); return true;
          case 'PageUp': movePanelCursor(-PAGE_ROWS); return true;
          case 'Enter': {
            const location = navPanel.locations[navPanel.cursor];
            if (location) follow(location);
            return true;
          }
          case 'Escape': return false;
        }
        return true;
      }
      if (shownOffer && event.key === 'Enter') {
        takeOffer(shownOffer);
        return true;
      }
      if (navNote && navNote.tone !== 'asking') setNavNote(null);
      const navKind = NAV_KEYS[event.key];
      if (navKind && !event.shiftKey) {
        startPicking(navKind);
        return true;
      }
      if (event.key === 's') return startSetup();
      if (event.key === 'x') return toggleSetupOffer();
      const frameKey = FRAME_KEYS.find(([key]) => key === event.key);
      if (frameKey) {
        switchFrame(frameKey[1]);
        return true;
      }
      const readOnly = openFileView?.target.outside === true;
      if (marks && readOnly && (event.key === 'i' || event.key === 'c' || event.key === 'a')) {
        setNotice(`${openFileView.target.path} is outside the repository, so it takes no edits or notes.`);
        return true;
      }
      if (marks && event.key === 'i') {
        startEdit();
        return true;
      }
      if (marks && event.key === 'u') return undoEdit();
      if (event.key === 'o' && openDocumentInEditor && !openFileView) {
        const path = currentTarget()?.getAttribute('data-path');
        if (!path || !isDocumentPath(path)) return false;
        openDocumentInEditor(path);
        return true;
      }
      if (marks && (event.key === 'c' || event.key === 'a')) {
        openComposer(event.key === 'a' ? 'question' : 'comment');
        return true;
      }
      const fileRows = openFileView?.rows;
      if (frame === 'walkthrough' || openFileView) {
        const stage = frame === 'walkthrough';
        if (event.shiftKey && (event.key === 'ArrowRight' || event.key === 'ArrowLeft')) {
          const body = fileBodyRef.current;
          body?.scrollBy({ left: (event.key === 'ArrowRight' ? 1 : -1) * body.clientWidth / 2 });
          return true;
        }
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
  }, [back, composer, currentTarget, editing, expandAtCursor, files.length, follow, frame, help, mapIndex, marks, messageStops.length, moveFileCursor, moveLine, movePanelCursor, moveStop, navNote, navPanel, openAtCursor, shownOffer, takeOffer, openComposer, openDocumentInEditor, openFileView, openSearch, pickLabel, picking, returnToOpenBox, rootRef, selectMessage, sendReview, sending, side, sides, startEdit, startPicking, startSetup, stepStage, switchFrame, switchSide, tileWindow, toggleSetupOffer, undoEdit]);

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
    () => ({ bundle, expansions, frame, stage, showStop: pickStage, openFile, openSearch, marksByLine, asking, removeMark, openInEditor: openDocumentInEditor, labels, asked, filesShown, toggleFiles }),
    [asked, asking, bundle, expansions, filesShown, frame, labels, marksByLine, stage, pickStage, openFile, openSearch, removeMark, openDocumentInEditor, toggleFiles],
  );

  return (
    <ReviewReaderContext.Provider value={context}>
      <div
        ref={frameRef}
        className={`review-frame review-frame--${frame}${frame === 'walkthrough' && filled ? ' review-frame--script-hidden' : ''}${frame === 'map' && !mapBeside ? ' review-frame--map-stacked' : ''}`}
        style={mapBeside ? { gridTemplateColumns: MAP_COLUMNS } : undefined}
      >
        <div className="review-doc" ref={docRef} onClick={onScopeClick}>
          {children}
          {movedMarks.length > 0 ? (
            <section className="review-marks-moved" aria-label="Moved notes">
              <h3>Moved notes</h3>
              <p>The lines these were written on changed. They still go with the review, marked as moved.</p>
              {movedMarks.map((mark) => (
                <div key={mark.id} className="review-marks-moved-entry">
                  <span className="review-send-place">{mark.fileAnchor ? fileAnchorPlace(mark.fileAnchor) : ''}</span>
                  <MarkRow mark={mark} asking={asking.get(mark.id)} onRemove={marks ? () => marks.deleteAnnotation(mark.id) : undefined} />
                </div>
              ))}
            </section>
          ) : null}
        </div>
        {frame === 'walkthrough' && openFileView ? (
          <FileView
            file={openFileView}
            tokenCache={tokenCache}
            filled={filled}
            bodyRef={fileBodyRef}
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
            onPick={pickMessage}
            onSide={switchSide}
          />
        ) : null}
        {frame !== 'walkthrough' && openFileView ? (
          <FileView
            file={openFileView}
            tokenCache={tokenCache}
            filled={filled}
            bodyRef={fileBodyRef}
            canGoBack={files.length > 1}
            onBack={back}
            onClose={closeFileView}
            onPick={(index) => moveFileCursor(() => index)}
          />
        ) : null}
        {notice ? <div className="review-notice" role="status">{notice}</div> : null}
        {composer && marks ? (
          <MarkComposer kind={composer.kind} target={composer.target} destination={marks.destination} onSave={saveMark} />
        ) : null}
      </div>
      {search ? <SearchPanel initialQuery={search.query} files={bundle.files} search={searchRepository} onPick={(target) => openFile(target)} /> : null}
      {help ? <ReviewKeySheet readOnly={openFileView?.target.outside === true} /> : null}
      {editing ? <EditBox key={`${editing.path}:${editing.hash}`} edit={editing} onSave={saveEdit} onTakeLine={takeLine} /> : null}
      {navPanel ? <NavPanel answer={navPanel} onPick={(index) => { const location = navPanel.locations[index]; if (location) follow(location); }} /> : null}
      {sending && marks ? (
        <SendSheet marks={annotations} verdict={sending.verdict} destination={marks.destination} onPick={(verdict) => setSending({ verdict })} onSend={sendReview} />
      ) : null}
      {picking ? (
        <div className="review-nav-note" role="status">{pickingText(picking.kind)} <kbd>esc</kbd> cancels.</div>
      ) : shownOffer ? (
        <OfferLine answer={shownOffer} />
      ) : navNote ? (
        <div className={`review-nav-note${navNote.tone === 'alert' ? ' review-nav-note--alert' : ''}`} role={navNote.tone === 'alert' ? 'alert' : 'status'}>{navNote.text}</div>
      ) : null}
      <ReviewKeyLine frame={frame} offered={offered} fileOpen={frame !== 'walkthrough' && openFileView !== null} sides={sides} marking={marks !== undefined && openFileView?.target.outside !== true} navigation={delivered.navigation} setup={setup} onFrame={switchFrame} />
    </ReviewReaderContext.Provider>
  );
}
