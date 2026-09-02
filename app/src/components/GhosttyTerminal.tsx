import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import {
  attachTerminalInput,
  CellFlags,
  type GhosttyCell,
  type GhosttyTerminal as GhosttyModel,
  type SnapshotHistoryDecoder,
} from '../ghostty';
import { loadGhostty } from '../ghostty/wasm';
import { openPath, openUrl } from '@tauri-apps/plugin-opener';
import { exists } from '@tauri-apps/plugin-fs';
import { homeDir } from '@tauri-apps/api/path';
import {
  fragmentAtColumn,
  hyperlinkRangeAt,
  logicalIndexForCell,
  logicalLineAt,
  pathCandidatesForFragment,
  resolveDetectedPath,
  spanFromLogicalRange,
  terminalLinkOpenAction,
  urlAtColumn,
  type DetectedTerminalLink,
  type LogicalLine,
  type LogicalSpan,
} from '../utils/terminalLinks';
import {
  initialFocusedMatch,
  startFindScan,
  visibleMatches,
  type FindMatch,
  type FindScanHandle,
} from '../utils/terminalFind';
import { emptyOsc133State, parseOsc133, type Osc133State } from '../utils/terminalOsc133';
import {
  blockViewportSpanAnchored,
  extractBlock,
  reanchorDelta,
  TerminalBlockStore,
  type BlockRowAccess,
  type BlockViewportSpan,
  type SeededBlock,
  type TerminalBlock,
} from '../utils/terminalBlocks';
import {
  filterBlockOutputLines,
  lineSegments,
  type FilteredBlockLine,
} from '../utils/terminalBlockFilter';
import { TerminalContextMenu, type TerminalContextMenuItem } from './TerminalContextMenu';
import {
  enableGraphemeClustering,
  ensureGraphemeClustering,
  writeReassertingClustering,
} from './terminalGraphemeMode';
import {
  cleanTerminalLines,
  terminalStyledSelectionToMarkdown,
  type TerminalMarkdownLine,
  type TerminalMarkdownRun,
} from '../utils/terminalMarkdown';
import { readClipboardText, writeClipboardText } from '../utils/clipboardBridge';
import { isMacLikePlatform, terminalClipboardChord } from '../shortcuts/platform';
import { formatShortcut, keyCombo } from '../shortcuts/formatShortcut';
import { parseOsc52Writes, type Osc52State } from '../utils/terminalOsc';
import {
  parseSynchronizedOutput,
  type SynchronizedOutputState,
} from '../utils/terminalSynchronizedOutput';
import {
  FONT_FAMILY,
  TERMINAL_SCROLLBACK_BYTES,
  getTerminalAnsiPalette,
  getTerminalTheme,
  type ResolvedTheme,
} from '../utils/terminalSizing';
import {
  createResizeCoalescer,
  resizeGhosttyWithoutReflow,
  type ResizeCoalescer,
  type TerminalDimensions,
} from '../utils/ghosttyResize';
import { stripDaemonOwnedResponses } from '../utils/terminalQueryResponses';
import {
  fitRequiresTerminalResize,
  fitShouldBailAsSuspicious,
  geometryOverflowsContainer,
  isWorkspaceResizeDragActive,
  recoveryDelayMs,
} from './ghosttyGeometry';
import { recordTerminalLinkHitTestEvent } from '../utils/terminalLinkHitTestLog';
import {
  recordDiag,
  recordPaint,
  noteModelFault,
  noteRecovery,
  noteResize,
  registerRenderProbe,
  disposePaneDiagnostics,
  TERMINAL_DIAGNOSTICS_FILE,
} from '../utils/terminalDiagnosticsLog';
import { createGhosttyModelOpRing, type ModelFaultCapture } from '../utils/ghosttyModelOpRing';
import { captureUiSnapshot, recordUiDiag, UI_DIAGNOSTICS_FILE } from '../utils/uiDiagnosticsLog';
import type { TerminalVisibleContentSnapshot } from '../utils/terminalVisibleContent';
import { analyzeTerminalVisibleLines } from '../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot, TerminalVisibleStyleLineSnapshot } from '../utils/terminalStyleSummary';
import { registerTerminalPerfGetter, type TerminalPerfStartupSnapshot } from '../utils/terminalPerf';
import { terminalOutputDelayMs } from '../utils/terminalOutputPacing';
import {
  applicationMouseInput,
  applicationWheelInput,
  bufferRowFromViewportRow,
  consumeWheelRows,
  createApplicationSelectionAnchor,
  offsetAfterWrite,
  relocateApplicationSelection,
  shouldReportApplicationMouseMove,
  viewportBufferStart,
  viewportRowFromBufferRow,
  type ApplicationSelectionAnchor,
} from '../utils/ghosttyScroll';
import {
  type MessageAnchor,
  type MessageRowAccess,
  type TerminalAnnotationStore,
} from '../utils/terminalAnnotations';
import { createTerminalKeyInterceptor } from './SessionTerminalWorkspace/terminalKeyHandler';
import {
  forgetTerminalInputLatencyRuntime,
  noteTerminalKeyEvent,
} from '../utils/terminalInputLatency';
import { ensureTerminalIconFont } from '../utils/terminalIconFont';
import {
  KittyPlacementStore,
  placementQuad,
  placementSourceRect,
} from '../utils/kittyPlacements';
import { kittyImageCache, type KittyImageStatus } from '../utils/kittyImageCache';
import type { PlacementElement, Seed } from '../types/generated';
import {
  WebGlTerminalRenderer,
  type WebGlImageQuad,
  type WebGlOverlay,
} from './GhosttyWebGlRenderer';
import {
  seedOccurrenceAtCell,
  seedOccurrenceSegments,
  seedOccurrencesInLine,
  type TerminalSeedOccurrence,
  type TerminalSeedSegment,
} from '../utils/terminalSeedLinks';
import {
  TerminalSeedPreview,
  type TerminalSeedAnchor,
} from './TerminalSeedPreview';
import './GhosttyTerminal.css';

export interface GhosttyTerminalProps {
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  debugName: string;
  cwd?: string;
  runtimeLogMeta?: {
    sessionId: string;
    paneId: string;
    runtimeId: string;
    paneKind: 'agent';
    isActivePane: boolean;
    isActiveSession: boolean;
    paneCount: number;
  };
  // 'user' keystrokes stamp the daemon's composer clock; 'pointer' (mouse
  // reports) and 'response' (terminal query replies) deliberately do not.
  onInput: (data: string, source?: string) => void;
  onPointerActivity?: () => void;
  onOpenMarkdown?: (path: string, sessionId: string) => void;
  gardenSeeds?: readonly Seed[];
  onOpenSeed?: (seedId: string) => void;
  onReady: (terminal: GhosttyTerminalHandle) => void;
  onResize: (cols: number, rows: number, options?: { reason?: string; xpixel?: number; ypixel?: number }) => void;
  onTerminalModelRecovered?: () => void;
  annotations?: TerminalAnnotationStore;
  annotationsVersion?: number;
  onAnnotationAnchor?: (
    anchor: MessageAnchor,
    at: { clientX: number; clientY: number },
  ) => void;
  onAnnotationMiss?: (
    reason: 'no-messages' | 'outside-messages',
    at: { clientX: number; clientY: number },
  ) => void;
  onAnnotationActivate?: (
    annotationId: string,
    at: { clientX: number; clientY: number },
  ) => void;
}

export interface GhosttyTerminalHandle {
  fit: () => void;
  openFind: () => void;
  focus: () => boolean;
  typeTextViaInput: (text: string) => boolean;
  isInputFocused: () => boolean;
  write: (
    data: string | Uint8Array,
    options?: {
      suppressResponses?: boolean;
      deferRender?: boolean;
    },
  ) => Promise<void>;
  resizeLocal: (
    cols: number,
    rows: number,
    options?: { restore?: boolean },
  ) => Promise<void>;
  restoreSnapshot: (snapshot: Uint8Array) => Promise<void>;
  seedBlocks: (blocks: SeededBlock[]) => Promise<void>;
  applyPlacements: (sessionId: string, seq: number, placements: PlacementElement[]) => Promise<void>;
  seedPlacements: (sessionId: string, placements: PlacementElement[]) => Promise<void>;
  reset: () => void;
  setSurfaceReleased: (released: boolean) => void;
  scrollToTop: () => boolean;
  getText: () => string;
  getSize: () => { cols: number; rows: number } | null;
  getBounds: () => DOMRect | null;
  hasMeasuredSize: () => boolean;
  overflowsContainer: () => boolean;
  getVisibleContent: () => TerminalVisibleContentSnapshot;
  getVisibleStyleSummary: () => TerminalVisibleStyleSnapshot;
  getBlockState: () => BlockStateSnapshot;
  getPlacementState: () => PlacementStateSnapshot;
  drain: () => Promise<void>;
}

export interface BlockStateSnapshotBlock {
  id: number;
  command: string;
  exitCode?: number;
  promptRow: number;
  outputStartRow?: number;
  endRow?: number;
  anchorRow: number;
  anchorText: string;
  reanchorDelta: number | null;
  viewportSpan: BlockViewportSpan | null;
}

export interface BlockStateSnapshot {
  cols: number;
  rows: number;
  scrollback: number;
  viewportOffset: number;
  firstViewportBufferRow: number;
  selectedBlockId: number | null;
  blocks: BlockStateSnapshotBlock[];
}

const EMPTY_BLOCK_STATE: BlockStateSnapshot = {
  cols: 0, rows: 0, scrollback: 0, viewportOffset: 0, firstViewportBufferRow: 0,
  selectedBlockId: null, blocks: [],
};

export interface PlacementStateSnapshotEntry {
  imageId: number;
  placementId: number;
  generation: number;
  z: number;
  bufferRow: number;
  col: number;
  pixelWidth: number;
  pixelHeight: number;
  sourceX: number;
  sourceY: number;
  sourceWidth: number;
  sourceHeight: number;
  screenRow: number;
  visible: boolean;
  blob: KittyImageStatus;
}

export interface PlacementStateSnapshot {
  sessionId: string;
  cols: number;
  rows: number;
  scrollback: number;
  viewportOffset: number;
  firstViewportBufferRow: number;
  /** -1 until a set has been applied; equal seqs are legal (a resize re-describes). */
  lastAppliedSeq: number;
  placements: PlacementStateSnapshotEntry[];
}

const EMPTY_PLACEMENT_STATE: PlacementStateSnapshot = {
  sessionId: '', cols: 0, rows: 0, scrollback: 0, viewportOffset: 0,
  firstViewportBufferRow: 0, lastAppliedSeq: -1, placements: [],
};

interface SelectionRange {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
}

// Ghostty's native renderer resets synchronized-output mode after 1000ms.
const SYNCHRONIZED_OUTPUT_RENDER_TIMEOUT_MS = 1000;

const ANNOTATION_COLOR = '#a78bfa';

interface HoverLinkState {
  generation: number;
  line: LogicalLine;
  startIndex: number;
  endIndex: number;
  link: DetectedTerminalLink | null;
  linkSpan: LogicalSpan | null;
}

interface VisibleSeedOccurrences {
  generation: number;
  seeds: readonly Seed[];
  occurrences: TerminalSeedOccurrence[];
}

interface SeedMarkLayout {
  signature: string;
  canvasLeft: number;
  canvasTop: number;
  cellWidth: number;
  cellHeight: number;
  segments: TerminalSeedSegment[];
}

interface SeedPreviewState {
  seedId: string;
  anchor: TerminalSeedAnchor;
}

const EMPTY_GARDEN_SEEDS: readonly Seed[] = [];
const SEED_PREVIEW_OPEN_DELAY_MS = 105;
const SEED_PREVIEW_CLOSE_DELAY_MS = 230;

function sameSeedAnchor(a: TerminalSeedAnchor, b: TerminalSeedAnchor): boolean {
  return a.left === b.left
    && a.right === b.right
    && a.top === b.top
    && a.bottom === b.bottom
    && a.bounds.left === b.bounds.left
    && a.bounds.right === b.bounds.right
    && a.bounds.top === b.bounds.top
    && a.bounds.bottom === b.bounds.bottom;
}

const utf8Encoder = new TextEncoder();

function isWorkspaceResizeActive(element: HTMLElement | null): boolean {
  if (document.documentElement.dataset.attnWorkspaceResizing === '1') {
    return true;
  }
  const suppressUntil = Number(document.documentElement.dataset.attnWorkspaceMouseSuppressUntil || 0);
  if (Number.isFinite(suppressUntil) && suppressUntil > Date.now()) {
    return true;
  }
  return Boolean(element?.closest('.session-terminal-panes[data-resizing-split-id]'));
}

function wordRangeAtColumn(line: string, col: number): { startCol: number; endCol: number } | null {
  const isWordCharacter = (character: string | undefined) => Boolean(character && /[\w-]/.test(character));
  if (!isWordCharacter(line[col])) return null;
  let startCol = col;
  while (startCol > 0 && isWordCharacter(line[startCol - 1])) startCol -= 1;
  let endCol = col + 1;
  while (endCol < line.length && isWordCharacter(line[endCol])) endCol += 1;
  return { startCol, endCol };
}

function rectSnapshot(rect: DOMRect | null) {
  if (!rect) return null;
  return {
    x: rect.x,
    y: rect.y,
    width: rect.width,
    height: rect.height,
    top: rect.top,
    left: rect.left,
    right: rect.right,
    bottom: rect.bottom,
  };
}

function cellFromRect(
  event: { clientX: number; clientY: number },
  rect: DOMRect | null,
  cellWidth: number,
  cellHeight: number,
  rows: number,
  cols: number,
) {
  if (!rect) return null;
  if (
    event.clientX < rect.left
    || event.clientX >= rect.right
    || event.clientY < rect.top
    || event.clientY >= rect.bottom
  ) {
    return null;
  }
  return {
    row: Math.max(0, Math.min(rows - 1, Math.floor((event.clientY - rect.top) / cellHeight))),
    col: Math.max(0, Math.min(cols, Math.floor((event.clientX - rect.left) / cellWidth))),
  };
}

function colorNumber(value: string): number {
  return Number.parseInt(value.slice(1), 16);
}

// getViewport()'s tail holds stale cells from a larger pre-resize grid, so count only cols*rows.
// A renderer-independent witness: numbers from one render pass can never catch under-drawing.
function countModelPrintable(terminal: GhosttyModel): number {
  const viewport = terminal.getViewport();
  const windowLen = terminal.cols * terminal.rows;
  let printable = 0;
  for (let i = 0; i < windowLen && i < viewport.length; i += 1) {
    const cell = viewport[i];
    if (cell && cell.codepoint > 32) printable += 1;
  }
  return printable;
}

function emptyStartup(): TerminalPerfStartupSnapshot {
  return {
    initialContainer: null,
    initialCols: null,
    initialRows: null,
    firstObservedContainer: null,
    firstReadySource: null,
    firstReadyAt: null,
    firstReadyCols: null,
    firstReadyRows: null,
    fontEffectAppliedBeforeReady: false,
    skippedInitialFontEffect: false,
  };
}

function createRendererEffectResources() {
  let recoveryTimer: ReturnType<typeof setTimeout> | null = null;
  let resizeObserver: ResizeObserver | null = null;

  return {
    isRecoveryScheduled: () => recoveryTimer !== null,
    scheduleRecovery: (callback: () => void, delayMs: number) => {
      if (recoveryTimer !== null) return;
      recoveryTimer = setTimeout(() => {
        recoveryTimer = null;
        callback();
      }, delayMs);
    },
    observeResize: (target: Element, callback: ResizeObserverCallback) => {
      resizeObserver = new ResizeObserver(callback);
      resizeObserver.observe(target);
    },
    dispose: () => {
      if (recoveryTimer !== null) {
        clearTimeout(recoveryTimer);
        recoveryTimer = null;
      }
      resizeObserver?.disconnect();
      resizeObserver = null;
    },
  };
}

function normalizeSelection(range: SelectionRange): SelectionRange {
  if (range.startRow < range.endRow || (range.startRow === range.endRow && range.startCol <= range.endCol)) {
    return range;
  }
  return {
    startRow: range.endRow,
    startCol: range.endCol,
    endRow: range.startRow,
    endCol: range.startCol,
  };
}

function cellText(
  terminal: GhosttyModel,
  cells: GhosttyCell[],
  row: number,
  startCol = 0,
  scrollback = false,
  trimEnd = true,
): string {
  let text = '';
  for (let offset = 0; offset < cells.length; offset += 1) {
    const col = startCol + offset;
    const cell = cells[offset];
    if (!cell || cell.width === 0) continue;
    text += cell.grapheme_len > 0
      ? scrollback
        ? terminal.getScrollbackGraphemeString(row, col)
        : terminal.getGraphemeString(row, col)
      : cell.codepoint > 0 ? String.fromCodePoint(cell.codepoint) : ' ';
  }
  return trimEnd ? text.trimEnd() : text;
}

export const GhosttyTerminal = forwardRef<GhosttyTerminalHandle, GhosttyTerminalProps>(
  function GhosttyTerminal({ fontSize, resolvedTheme = 'dark', debugName, cwd, runtimeLogMeta, onInput, onPointerActivity, onOpenMarkdown, gardenSeeds = EMPTY_GARDEN_SEEDS, onOpenSeed, onReady, onResize, onTerminalModelRecovered, annotations, annotationsVersion = 0, onAnnotationAnchor, onAnnotationMiss, onAnnotationActivate }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const terminalRef = useRef<GhosttyModel | null>(null);
    const rendererRef = useRef<WebGlTerminalRenderer | null>(null);
    const surfaceReleasedRef = useRef(false);
    const inputRef = useRef<(() => void) | null>(null);
    const modelSizeRef = useRef({ cols: 80, rows: 24 });
    const hasMeasuredSizeRef = useRef(false);
    const viewportOffsetRef = useRef(0);
    const wheelRemainderRowsRef = useRef(0);
    const selectionRef = useRef<SelectionRange | null>(null);
    const selectedTextRef = useRef<string | null>(null);
    const applicationSelectionAnchorRef = useRef<ApplicationSelectionAnchor | null>(null);
    const selectingRef = useRef(false);
    const selectionPointerStartRef = useRef<{ clientX: number; clientY: number } | null>(null);
    const selectionDragThresholdMetRef = useRef(false);
    const selectionDragCleanupRef = useRef<(() => void) | null>(null);
    const trackedMouseButtonRef = useRef<number | null>(null);
    const trackedMouseCellRef = useRef<{ row: number; col: number } | null>(null);
    const trackedMouseReleaseCleanupRef = useRef<(() => void) | null>(null);
    const hoveredCellRef = useRef<{ row: number; col: number } | null>(null);
    const acceleratorHeldRef = useRef(false);
    const cwdRef = useRef(cwd);
    const hoverGenerationRef = useRef(0);
    const hoverLinkRef = useRef<HoverLinkState | null>(null);
    const refreshHoverLinkRef = useRef<(() => void) | null>(null);
    const visibleSeedOccurrencesRef = useRef<VisibleSeedOccurrences | null>(null);
    const refreshSeedOccurrencesRef = useRef<(() => void) | null>(null);
    const syncSeedPreviewAfterRefreshRef = useRef<(() => void) | null>(null);
    const gardenSeedsRef = useRef(gardenSeeds);
    const onOpenSeedRef = useRef(onOpenSeed);
    const seedPreviewRef = useRef<SeedPreviewState | null>(null);
    const seedPreviewOpenTimerRef = useRef<number | null>(null);
    const seedPreviewCloseTimerRef = useRef<number | null>(null);
    const seedPreviewPendingIdRef = useRef<string | null>(null);
    const seedPreviewPointerInsideRef = useRef(false);
    const homeDirRef = useRef<string | null | undefined>(undefined);
    const pathExistsCacheRef = useRef(new Map<string, boolean | Promise<boolean>>());
    const findOpenRef = useRef(false);
    const findQueryRef = useRef('');
    const findCaseSensitiveRef = useRef(false);
    const findMatchesRef = useRef<FindMatch[]>([]);
    const findFocusedIndexRef = useRef(-1);
    const findScanRef = useRef<FindScanHandle | null>(null);
    const findRescanTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const findInputRef = useRef<HTMLInputElement>(null);
    const runFindScanRef = useRef<(() => void) | null>(null);
    const osc133StateRef = useRef<Osc133State>(emptyOsc133State());
    const graphemeResetCarryRef = useRef(false);
    // useRef(new Store()) would construct a store on every render; useState's lazy initializer builds it once.
    const [blockStore] = useState(() => new TerminalBlockStore());
    const blockStoreRef = useRef(blockStore);
    const selectedBlockIdRef = useRef<number | null>(null);
    const [placementStore] = useState(() => new KittyPlacementStore());
    const placementStoreRef = useRef(placementStore);
    const placementSessionRef = useRef('');
    const writeChainRef = useRef(Promise.resolve());
    const [modelOpRing] = useState(createGhosttyModelOpRing);
    const modelOpRingRef = useRef(modelOpRing);
    const fitResizeCoalescerRef = useRef<ResizeCoalescer | null>(null);
    const fitRef = useRef<() => void>(() => undefined);
    const overflowRefitRafRef = useRef<number | null>(null);
    const applyFitDimensionsRef = useRef<(dimensions: TerminalDimensions) => void>(() => undefined);
    const osc52StateRef = useRef<Osc52State>({ pending: '' });
    const synchronizedOutputStateRef = useRef<SynchronizedOutputState>({ active: false, pending: '' });
    const synchronizedOutputRenderTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const scheduledOutputRenderRef = useRef<number | null>(null);
    const scheduledOutputRenderForceRef = useRef(false);
    const scheduledScrollRenderRef = useRef<number | null>(null);
    const lastScheduledOutputPaintAtRef = useRef<number | null>(null);
    const renderCountRef = useRef(0);
    const renderCpuTotalMsRef = useRef(0);
    const renderCpuMaxMsRef = useRef(0);
    const lastRenderCpuMsRef = useRef(0);
    const renderFullCountRef = useRef(0);
    const renderPartialCountRef = useRef(0);
    const renderRowsPaintedRef = useRef(0);
    const renderSubmittedQuadsRef = useRef(0);
    const renderRetainedRowVertexBytesRef = useRef(0);
    const renderRetainedStagingBytesRef = useRef(0);
    const scheduledRenderRequestsRef = useRef(0);
    const scheduledRenderCoalescedRef = useRef(0);
    const scheduledRenderDeferredRef = useRef(0);
    const writeCountRef = useRef(0);
    const modelInstanceRef = useRef(0);
    const lastPaintQuadsRef = useRef(0);
    const lastModelPrintableRef = useRef(0);
    const lastRenderAtRef = useRef(0);
    const lastWriteAtRef = useRef(0);
    const readyRef = useRef(false);
    const startupRef = useRef(emptyStartup());
    const onInputRef = useRef(onInput);
    const onPointerActivityRef = useRef(onPointerActivity);
    const lastPointerActivityAtRef = useRef(Number.NEGATIVE_INFINITY);
    const onOpenMarkdownRef = useRef(onOpenMarkdown);
    const onReadyRef = useRef(onReady);
    const onResizeRef = useRef(onResize);
    const onTerminalModelRecoveredRef = useRef(onTerminalModelRecovered);
    const annotationsRef = useRef(annotations);
    const onAnnotationAnchorRef = useRef(onAnnotationAnchor);
    const onAnnotationMissRef = useRef(onAnnotationMiss);
    const onAnnotationActivateRef = useRef(onAnnotationActivate);
    const annotationDragRef = useRef(false);
    const annotationClickRef = useRef<string | null>(null);
    const annotationHoverRef = useRef<string | null>(null);
    const altHeldRef = useRef(false);
    const runtimeMetaRef = useRef(runtimeLogMeta);
    const debugNameRef = useRef(debugName);
    const diagKeyRef = useRef<string>(runtimeLogMeta?.paneId ?? runtimeLogMeta?.sessionId ?? debugName);
    // A canvas keeps returning the same dead context from getContext(), so only a
    // fresh keyed element recovers from a lost one.
    const [rendererEpoch, setRendererEpoch] = useState(0);
    const recoveryAttemptRef = useRef(0);
    const modelFaultDedupeRef = useRef<{
      operation: string;
      error: string;
      model: number;
      rendererEpoch: number;
    } | null>(null);
    const modelRecoveryPendingRef = useRef(false);
    const [error, setError] = useState<string | null>(null);
    const [linkCursorActive, setLinkCursorActive] = useState(false);
    const [seedMarkLayout, setSeedMarkLayout] = useState<SeedMarkLayout | null>(null);
    const [seedPreview, setSeedPreview] = useState<SeedPreviewState | null>(null);
    const [annotationCursorActive, setAnnotationCursorActive] = useState(false);
    const [findUi, setFindUi] = useState({ open: false, matchCount: 0, focusedIndex: -1, scanning: false, caseSensitive: false });
    const [contextMenu, setContextMenu] = useState<{ x: number; y: number; blockId: number | null } | null>(null);
    const [filterUi, setFilterUi] = useState<{ open: boolean; blockId: number | null; caseSensitive: boolean }>({ open: false, blockId: null, caseSensitive: false });
    const [filterMatches, setFilterMatches] = useState<FilteredBlockLine[]>([]);
    const filterQueryRef = useRef('');
    const filterInputRef = useRef<HTMLInputElement>(null);
    const filterRescanTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const fontSizeRef = useRef(fontSize);

    onInputRef.current = onInput;
    onOpenMarkdownRef.current = onOpenMarkdown;
    onReadyRef.current = onReady;
    onResizeRef.current = onResize;
    onTerminalModelRecoveredRef.current = onTerminalModelRecovered;
    annotationsRef.current = annotations;
    onAnnotationAnchorRef.current = onAnnotationAnchor;
    onAnnotationMissRef.current = onAnnotationMiss;
    onAnnotationActivateRef.current = onAnnotationActivate;
    runtimeMetaRef.current = runtimeLogMeta;
    debugNameRef.current = debugName;
    cwdRef.current = cwd;
    fontSizeRef.current = fontSize;
    diagKeyRef.current = runtimeLogMeta?.paneId ?? runtimeLogMeta?.sessionId ?? debugName;

    const inputLatencyRuntimeId = runtimeLogMeta?.runtimeId;
    useEffect(() => {
      if (!inputLatencyRuntimeId) return;
      return () => forgetTerminalInputLatencyRuntime(inputLatencyRuntimeId);
    }, [inputLatencyRuntimeId]);

    useEffect(() => {
      onPointerActivityRef.current = onPointerActivity;
    }, [onPointerActivity]);

    useLayoutEffect(() => {
      gardenSeedsRef.current = gardenSeeds;
      onOpenSeedRef.current = onOpenSeed;
    }, [gardenSeeds, onOpenSeed]);

    const recoverFromModelFault = useCallback((operation: string, reason: unknown) => {
      if (modelFaultDedupeRef.current?.rendererEpoch === rendererEpoch) return;
      const error = reason instanceof Error ? reason.message : String(reason);
      const stack = reason instanceof Error ? reason.stack : undefined;
      const terminal = terminalRef.current;
      let cols: number | undefined;
      let rows: number | undefined;
      try {
        cols = terminal?.cols;
        rows = terminal?.rows;
      } catch {
      }
      const fault = {
        operation,
        error,
        model: modelInstanceRef.current,
        rendererEpoch,
      };
      modelFaultDedupeRef.current = fault;
      modelRecoveryPendingRef.current = true;
      const session = runtimeMetaRef.current?.sessionId ?? undefined;
      const paneKind = runtimeMetaRef.current?.paneKind ?? undefined;
      let snapshot: Record<string, unknown> | undefined;
      try {
        snapshot = captureUiSnapshot();
      } catch {
      }
      let capture: ModelFaultCapture | undefined;
      try {
        capture = modelOpRingRef.current.capture();
      } catch {
      }
      noteModelFault(diagKeyRef.current, {
        session,
        paneKind,
        ...fault,
        stack,
        cols,
        rows,
        capture,
      });
      noteRecovery(diagKeyRef.current, {
        session,
        paneKind,
        attempt: 1,
        outcome: 'modelFault',
        error,
      });
      recordUiDiag({
        kind: 'ghostty_model_fault',
        diagnosticFile: UI_DIAGNOSTICS_FILE,
        captureIn: TERMINAL_DIAGNOSTICS_FILE,
        pane: diagKeyRef.current,
        session,
        paneKind,
        ...fault,
        stack,
        cols,
        rows,
        snapshot,
      });
      setError(null);
      setRendererEpoch((value) => value + 1);
    }, [rendererEpoch]);

    const getViewportCells = useCallback((): GhosttyCell[] | undefined => {
      const terminal = terminalRef.current;
      if (!terminal || viewportOffsetRef.current === 0) return undefined;
      const active = terminal.getViewport();
      const history = terminal.getScrollbackLength();
      const start = viewportBufferStart(history, viewportOffsetRef.current);
      const cells: GhosttyCell[] = [];
      for (let row = start; row < start + terminal.rows; row += 1) {
        const line = row < history ? terminal.getScrollbackLine(row) : active.slice((row - history) * terminal.cols, (row - history + 1) * terminal.cols);
        if (line) cells.push(...line);
      }
      return cells;
    }, []);

    const renderSurface = useCallback((force = false) => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      if (!terminal || !renderer) return true;
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) return true;
      try {
      refreshHoverLinkRef.current?.();
      refreshSeedOccurrencesRef.current?.();
      const range = selectionRef.current ? normalizeSelection(selectionRef.current) : null;
      const scrollbackLength = terminal.getScrollbackLength();
      const overlays: WebGlOverlay[] = [];
      if (range) {
        overlays.push({
          startRow: viewportRowFromBufferRow(range.startRow, scrollbackLength, viewportOffsetRef.current),
          startCol: range.startCol,
          endRow: viewportRowFromBufferRow(range.endRow, scrollbackLength, viewportOffsetRef.current),
          endCol: range.endCol,
          color: getTerminalTheme(resolvedTheme).selectionBackground,
          kind: 'background',
        });
      }
      const hover = hoverLinkRef.current;
      if (hover?.link && hover.linkSpan && hover.generation === hoverGenerationRef.current) {
        overlays.push({
          startRow: hover.linkSpan.startRow,
          startCol: hover.linkSpan.startCol,
          endRow: hover.linkSpan.endRow,
          endCol: hover.linkSpan.endCol,
          color: getTerminalTheme(resolvedTheme).foreground,
          alpha: 0.8,
          kind: 'underline',
        });
      }
      if (selectedBlockIdRef.current !== null) {
        const block = blockStoreRef.current.blockById(selectedBlockIdRef.current);
        const access = blockRowAccess();
        const span = block && access
          ? blockViewportSpanAnchored(
              block,
              access,
              viewportBufferStart(scrollbackLength, viewportOffsetRef.current),
              terminal.rows,
            )
          : null;
        if (!span) {
          selectedBlockIdRef.current = null;
        } else if (span.visible) {
          if (!span.spansViewport) {
            overlays.push({
              startRow: span.startRow,
              startCol: 0,
              endRow: span.endRow,
              endCol: terminal.cols,
              color: '#4d9de0',
              alpha: 0.08,
              kind: 'background',
            });
          }
          overlays.push({
            startRow: span.startRow,
            startCol: 0,
            endRow: span.endRow,
            endCol: terminal.cols,
            color: '#4d9de0',
            alpha: 0.9,
            kind: 'outline',
          });
        }
      }
      if (findOpenRef.current && findMatchesRef.current.length > 0) {
        const firstRow = viewportBufferStart(scrollbackLength, viewportOffsetRef.current);
        const focused = findFocusedIndexRef.current >= 0
          ? findMatchesRef.current[findFocusedIndexRef.current]
          : null;
        for (const match of visibleMatches(findMatchesRef.current, firstRow, terminal.rows)) {
          overlays.push({
            startRow: match.bufferRow - firstRow,
            startCol: match.startCol,
            endRow: match.bufferRow - firstRow,
            endCol: match.endCol,
            color: '#f5c542',
            alpha: match === focused ? 0.6 : 0.28,
            kind: 'background',
          });
        }
      }
      const annotationStore = annotationsRef.current;
      const annotationAccess = annotationStore ? messageRowAccess() : null;
      if (annotationStore && annotationAccess) {
        const firstRow = viewportBufferStart(scrollbackLength, viewportOffsetRef.current);
        for (const wash of annotationStore.project(annotationAccess)) {
          const hovered = altHeldRef.current && annotationHoverRef.current === wash.annotationId;
          for (const range of wash.rows) {
            const viewportRow = range.row - firstRow;
            if (viewportRow < 0 || viewportRow >= terminal.rows) continue;
            overlays.push({
              startRow: viewportRow,
              startCol: range.startCol,
              endRow: viewportRow,
              endCol: range.endCol,
              color: ANNOTATION_COLOR,
              alpha: hovered ? 0.42 : 0.24,
              kind: 'background',
            });
            overlays.push({
              startRow: viewportRow,
              startCol: range.startCol,
              endRow: viewportRow,
              endCol: range.endCol,
              color: ANNOTATION_COLOR,
              alpha: hovered ? 1 : 0.75,
              kind: 'underline',
            });
          }
        }
      }
      const imageQuads: WebGlImageQuad[] = [];
      const placed = placementStoreRef.current.placements();
      if (placed.length > 0) {
        const firstRow = viewportBufferStart(scrollbackLength, viewportOffsetRef.current);
        const gridWidth = terminal.cols * renderer.cellWidth;
        const gridHeight = terminal.rows * renderer.cellHeight;
        for (const placement of placed) {
          const quad = placementQuad(
            placement,
            firstRow,
            renderer.cellWidth,
            renderer.cellHeight,
            gridWidth,
            gridHeight,
            renderer.dpr,
          );
          if (!quad) continue;
          const blob = kittyImageCache.get(
            placementSessionRef.current,
            placement.imageId,
            placement.generation,
          );
          if (!blob) continue;
          const rect = placementSourceRect(quad, blob.width, blob.height);
          imageQuads.push({
            source: {
              imageId: blob.imageId,
              generation: blob.generation,
              width: blob.width,
              height: blob.height,
              format: blob.format,
              pixels: blob.pixels,
            },
            x: quad.x,
            y: quad.y,
            width: quad.width,
            height: quad.height,
            sourceX: rect.x,
            sourceY: rect.y,
            sourceWidth: rect.width,
            sourceHeight: rect.height,
            z: placement.z,
          });
        }
      }
      const sample = renderer.render(terminal, force, getViewportCells(), overlays, viewportOffsetRef.current, imageQuads);
      if (sample) {
        renderCountRef.current += 1;
        renderCpuTotalMsRef.current += sample.cpuSubmitMs;
        renderCpuMaxMsRef.current = Math.max(renderCpuMaxMsRef.current, sample.cpuSubmitMs);
        lastRenderCpuMsRef.current = sample.cpuSubmitMs;
        renderRowsPaintedRef.current += sample.paintedRows;
        renderSubmittedQuadsRef.current += sample.submittedQuads;
        renderRetainedRowVertexBytesRef.current = sample.retainedRowVertexBytes;
        renderRetainedStagingBytesRef.current = sample.retainedStagingBytes;
        if (sample.fullPaint) renderFullCountRef.current += 1;
        else renderPartialCountRef.current += 1;
        lastRenderAtRef.current = Date.now();
        lastPaintQuadsRef.current = sample.quads;
        lastModelPrintableRef.current = sample.modelPrintable;
      }
      recordPaint({
        pane: diagKeyRef.current,
        session: runtimeMetaRef.current?.sessionId ?? undefined,
        cols: terminal.cols,
        rows: terminal.rows,
        force,
        offset: viewportOffsetRef.current,
        modelPrintable: sample?.modelPrintable ?? lastModelPrintableRef.current,
        quads: sample ? sample.quads : null,
        cellsArrayLen: sample ? sample.cellsArrayLen : null,
        skipNull: sample ? sample.printableSkippedNull : null,
        skipZeroWidth: sample ? sample.printableSkippedZeroWidth : null,
      });
      return true;
      } catch (reason) {
        // A WASM trap in renderer.render() used to escape React and unmount the whole app.
        recoverFromModelFault('render', reason);
        return false;
      }
    }, [getViewportCells, recoverFromModelFault, resolvedTheme]);

    const setSurfaceReleased = useCallback((released: boolean) => {
      if (surfaceReleasedRef.current === released) return;
      surfaceReleasedRef.current = released;
      const renderer = rendererRef.current;
      if (!renderer) return;
      if (released) {
        renderer.releaseDrawingBuffer();
        return;
      }
      renderer.restoreDrawingBuffer();
      renderSurface(true);
    }, [renderSurface]);

    useEffect(() => {
      renderSurface(true);
    }, [annotations, annotationsVersion, renderSurface]);

    const clearSynchronizedOutputRenderTimer = useCallback(() => {
      if (!synchronizedOutputRenderTimerRef.current) return;
      clearTimeout(synchronizedOutputRenderTimerRef.current);
      synchronizedOutputRenderTimerRef.current = null;
    }, []);

    const cancelScheduledOutputRender = useCallback(() => {
      if (scheduledOutputRenderRef.current !== null) {
        cancelAnimationFrame(scheduledOutputRenderRef.current);
        scheduledOutputRenderRef.current = null;
      }
      scheduledOutputRenderForceRef.current = false;
      if (scheduledScrollRenderRef.current !== null) {
        cancelAnimationFrame(scheduledScrollRenderRef.current);
        scheduledScrollRenderRef.current = null;
      }
    }, []);

    // A wheel gesture outruns the display (120 Hz trackpad plus a momentum tail), and
    // painting per event queues more work than the main thread can drain.
    const scheduleScrollRender = useCallback(() => {
      if (scheduledScrollRenderRef.current !== null) return;
      scheduledScrollRenderRef.current = requestAnimationFrame(() => {
        scheduledScrollRenderRef.current = null;
        renderSurface(true);
      });
    }, [renderSurface]);

    // `force` is not optional on purpose: a default would silently pick one of the
    // two paints for the next caller.
    const scheduleOutputRender = useCallback((force: boolean) => {
      scheduledRenderRequestsRef.current += 1;
      scheduledOutputRenderForceRef.current ||= force;
      if (scheduledOutputRenderRef.current !== null) {
        scheduledRenderCoalescedRef.current += 1;
        return;
      }

      const requestPaint = (timestamp: number) => {
        const delayMs = terminalOutputDelayMs(timestamp, lastScheduledOutputPaintAtRef.current);
        if (delayMs > 1) {
          scheduledRenderDeferredRef.current += 1;
          scheduledOutputRenderRef.current = requestAnimationFrame(requestPaint);
          return;
        }
        scheduledOutputRenderRef.current = null;
        lastScheduledOutputPaintAtRef.current = timestamp;
        const forcePaint = scheduledOutputRenderForceRef.current;
        scheduledOutputRenderForceRef.current = false;
        renderSurface(forcePaint);
      };
      scheduledOutputRenderRef.current = requestAnimationFrame(requestPaint);
    }, [renderSurface]);

    const flushSynchronizedOutputRender = useCallback(() => {
      clearSynchronizedOutputRenderTimer();
      scheduleOutputRender(false);
    }, [clearSynchronizedOutputRenderTimer, scheduleOutputRender]);

    const scheduleSynchronizedOutputRenderFallback = useCallback(() => {
      if (synchronizedOutputRenderTimerRef.current) return;
      synchronizedOutputRenderTimerRef.current = setTimeout(() => {
        synchronizedOutputRenderTimerRef.current = null;
        synchronizedOutputStateRef.current = { active: false, pending: '' };
        scheduleOutputRender(false);
      }, SYNCHRONIZED_OUTPUT_RENDER_TIMEOUT_MS);
    }, [scheduleOutputRender]);

    // Reads one row: reassembling the whole viewport here cost it seven times over per wheel step.
    const lineAtVisibleRow = useCallback((row: number): string => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const history = terminal.getScrollbackLength();
      const bufferRow = bufferRowFromViewportRow(row, history, viewportOffsetRef.current);
      if (bufferRow < history) {
        const line = terminal.getScrollbackLine(bufferRow);
        return line ? cellText(terminal, line, bufferRow, 0, true) : '';
      }
      const activeRow = bufferRow - history;
      const line = terminal.getActiveLine(activeRow);
      return line ? cellText(terminal, line, activeRow, 0, false) : '';
    }, []);

    const selectionLineAtBufferRow = useCallback((row: number, startCol: number, endCol: number): string => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const history = terminal.getScrollbackLength();
      if (row < history) {
        const line = terminal.getScrollbackLine(row);
        return line ? cellText(terminal, line.slice(startCol, endCol), row, startCol, true, false) : '';
      }
      const viewportRow = row - history;
      if (viewportRow < 0 || viewportRow >= terminal.rows) return '';
      const active = terminal.getActiveLine(viewportRow);
      if (!active) return '';
      return cellText(
        terminal,
        active.slice(startCol, endCol),
        viewportRow,
        startCol,
        false,
        false,
      );
    }, []);

    const messageRowAccess = useCallback((): MessageRowAccess | null => {
      const terminal = terminalRef.current;
      if (!terminal) return null;
      return {
        cols: () => terminal.cols,
        totalRows: () => terminal.getScrollbackLength() + terminal.rows,
        rowText: (row) => selectionLineAtBufferRow(row, 0, terminal.cols),
        rowTextRange: (row, startCol, endCol) => selectionLineAtBufferRow(row, startCol, endCol),
        hyperlinkUri: (row, col) => {
          const history = terminal.getScrollbackLength();
          return row < history
            ? terminal.getScrollbackHyperlinkUri(row, col)
            : terminal.getHyperlinkUri(row - history, col);
        },
      };
    }, [selectionLineAtBufferRow]);

    const annotationAtCell = useCallback((cell: { row: number; col: number } | null): string | null => {
      const terminal = terminalRef.current;
      const store = annotationsRef.current;
      if (!terminal || !cell || !store || !store.hasWork()) return null;
      const access = messageRowAccess();
      if (!access) return null;
      const bufferRow = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
      return store.annotationAt(access, bufferRow, cell.col);
    }, [messageRowAccess]);

    const textForSelectionRange = useCallback((selection: SelectionRange | null) => {
      const terminal = terminalRef.current;
      const range = selection ? normalizeSelection(selection) : null;
      if (!terminal || !range) return '';
      const lines: string[] = [];
      for (let row = range.startRow; row <= range.endRow; row += 1) {
        const start = row === range.startRow ? range.startCol : 0;
        const end = row === range.endRow ? range.endCol : terminal.cols;
        lines.push(selectionLineAtBufferRow(row, start, end));
      }
      return cleanTerminalLines(lines).join('\n');
    }, [selectionLineAtBufferRow]);

    const selectedMarkdown = useCallback(() => {
      const terminal = terminalRef.current;
      const range = selectionRef.current ? normalizeSelection(selectionRef.current) : null;
      if (!terminal || !range) return '';
      const history = terminal.getScrollbackLength();
      const defaultForeground = colorNumber(getTerminalTheme(resolvedTheme).foreground);
      const lines: TerminalMarkdownLine[] = [];
      for (let row = range.startRow; row <= range.endRow; row += 1) {
        const start = row === range.startRow ? range.startCol : 0;
        const end = row === range.endRow ? range.endCol : terminal.cols;
        const scrollback = row < history;
        const activeRow = row - history;
        const cells = scrollback
          ? terminal.getScrollbackLine(row)?.slice(start, end) ?? []
          : terminal.getViewport().slice(activeRow * terminal.cols + start, activeRow * terminal.cols + end);
        const runs: TerminalMarkdownRun[] = [];
        for (let offset = 0; offset < cells.length; offset += 1) {
          const cell = cells[offset];
          if (!cell || cell.width === 0) continue;
          const text = cell.grapheme_len > 0
            ? scrollback
              ? terminal.getScrollbackGraphemeString(row, start + offset)
              : terminal.getGraphemeString(activeRow, start + offset)
            : cell.codepoint > 0 ? String.fromCodePoint(cell.codepoint) : ' ';
          const run = {
            text,
            bold: Boolean(cell.flags & CellFlags.BOLD),
            italic: Boolean(cell.flags & CellFlags.ITALIC),
            strikethrough: Boolean(cell.flags & CellFlags.STRIKETHROUGH),
            underline: Boolean(cell.flags & CellFlags.UNDERLINE),
            colored: (cell.fg_r << 16 | cell.fg_g << 8 | cell.fg_b) !== defaultForeground,
          };
          const current = runs[runs.length - 1];
          if (current
            && current.bold === run.bold
            && current.italic === run.italic
            && current.strikethrough === run.strikethrough
            && current.underline === run.underline
            && current.colored === run.colored) {
            current.text += run.text;
          } else {
            runs.push(run);
          }
        }
        // `wrapped` belongs to the row above, and a scrollback row carries no flag.
        const wrapped = row > 0 && (activeRow > 0
          ? terminal.rowWrapsIntoNext(activeRow - 1)
          : selectionLineAtBufferRow(row - 1, 0, terminal.cols).length === terminal.cols);
        lines.push({ runs, wrapped });
      }
      return terminalStyledSelectionToMarkdown(lines);
    }, [resolvedTheme, selectionLineAtBufferRow]);

    const getText = useCallback(() => {
      const terminal = terminalRef.current;
      if (!terminal) return '';
      const lines: string[] = [];
      for (let row = 0; row < terminal.getScrollbackLength(); row += 1) {
        const line = terminal.getScrollbackLine(row);
        if (line) lines.push(cellText(terminal, line, row, 0, true));
      }
      const active = terminal.getViewport();
      for (let row = 0; row < terminal.rows; row += 1) {
        lines.push(cellText(terminal, active.slice(row * terminal.cols, (row + 1) * terminal.cols), row));
      }
      return lines.join('\n');
    }, []);

    const getVisibleContent = useCallback((): TerminalVisibleContentSnapshot => {
      const terminal = terminalRef.current;
      if (!terminal) return { cols: null, viewportY: null, lineCount: 0, lines: [], lineMetrics: [], summary: analyzeTerminalVisibleLines([]) };
      const lines = Array.from({ length: terminal.rows }, (_, row) => lineAtVisibleRow(row));
      return {
        cols: terminal.cols,
        viewportY: Math.max(0, terminal.getScrollbackLength() - viewportOffsetRef.current),
        lineCount: lines.length,
        lines,
        lineMetrics: lines.map((text, rowOffset) => ({
          rowOffset,
          text,
          occupiedColumns: text.length,
          occupiedWidthRatio: terminal.cols > 0 ? text.length / terminal.cols : 0,
          nonEmpty: text.trim().length > 0,
        })),
        summary: analyzeTerminalVisibleLines(lines, terminal.cols),
      };
    }, [lineAtVisibleRow]);

    const getVisibleStyleSummary = useCallback((): TerminalVisibleStyleSnapshot => {
      const terminal = terminalRef.current;
      if (!terminal) {
        return { cols: null, rows: null, viewportY: null, lineCount: 0, lines: [], summary: { styledCellCount: 0, styledLineCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0, uniqueStyleCount: 0 } };
      }
      const cells = getViewportCells() ?? terminal.getViewport();
      const lines: TerminalVisibleStyleLineSnapshot[] = [];
      const summary = { styledCellCount: 0, styledLineCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0, uniqueStyleCount: 0 };
      const styles = new Set<string>();
      for (let row = 0; row < terminal.rows; row += 1) {
        const rowCells = cells.slice(row * terminal.cols, (row + 1) * terminal.cols);
        const line: TerminalVisibleStyleLineSnapshot = { rowOffset: row, text: lineAtVisibleRow(row), styledCellCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0 };
        rowCells.forEach((cell) => {
          if (!cell || cell.width === 0 || cell.codepoint === 0 || cell.codepoint === 32) return;
          const bold = Boolean(cell.flags & CellFlags.BOLD);
          const italic = Boolean(cell.flags & CellFlags.ITALIC);
          const underline = Boolean(cell.flags & CellFlags.UNDERLINE);
          const inverse = Boolean(cell.flags & CellFlags.INVERSE);
          const colored = cell.fg_r !== 0 || cell.fg_g !== 0 || cell.fg_b !== 0 || cell.bg_r !== 0 || cell.bg_g !== 0 || cell.bg_b !== 0;
          if (!bold && !italic && !underline && !inverse && !colored) return;
          line.styledCellCount += 1; summary.styledCellCount += 1;
          if (bold) { line.boldCellCount += 1; summary.boldCellCount += 1; }
          if (italic) { line.italicCellCount += 1; summary.italicCellCount += 1; }
          if (underline) { line.underlineCellCount += 1; summary.underlineCellCount += 1; }
          if (inverse) { line.inverseCellCount += 1; summary.inverseCellCount += 1; }
          if (cell.fg_r || cell.fg_g || cell.fg_b) { line.fgRgbCellCount += 1; summary.fgRgbCellCount += 1; }
          if (cell.bg_r || cell.bg_g || cell.bg_b) { line.bgRgbCellCount += 1; summary.bgRgbCellCount += 1; }
          styles.add(`${cell.flags}:${cell.fg_r},${cell.fg_g},${cell.fg_b}:${cell.bg_r},${cell.bg_g},${cell.bg_b}`);
        });
        if (line.styledCellCount > 0) summary.styledLineCount += 1;
        lines.push(line);
      }
      summary.uniqueStyleCount = styles.size;
      return { cols: terminal.cols, rows: terminal.rows, viewportY: viewportOffsetRef.current, lineCount: lines.length, lines, summary };
    }, [getViewportCells, lineAtVisibleRow]);

    const scrollToFindMatch = useCallback((match: FindMatch) => {
      const terminal = terminalRef.current;
      if (!terminal) return;
      const history = terminal.getScrollbackLength();
      const firstVisible = viewportBufferStart(history, viewportOffsetRef.current);
      if (match.bufferRow >= firstVisible && match.bufferRow < firstVisible + terminal.rows) return;
      viewportOffsetRef.current = Math.max(0, Math.min(history, history - match.bufferRow));
      wheelRemainderRowsRef.current = 0;
      hoverGenerationRef.current += 1;
    }, []);

    const runFindScan = useCallback(() => {
      findScanRef.current?.cancel();
      findScanRef.current = null;
      const terminal = terminalRef.current;
      if (!terminal || !findOpenRef.current) return;
      const query = findQueryRef.current;
      if (!query) {
        findMatchesRef.current = [];
        findFocusedIndexRef.current = -1;
        setFindUi((ui) => ({ ...ui, matchCount: 0, focusedIndex: -1, scanning: false }));
        renderSurface(true);
        return;
      }
      setFindUi((ui) => ({ ...ui, scanning: true }));
      const access = {
        totalRows: () => {
          const model = terminalRef.current;
          return model ? model.getScrollbackLength() + model.rows : 0;
        },
        rowText: (bufferRow: number) => {
          const model = terminalRef.current;
          return model ? selectionLineAtBufferRow(bufferRow, 0, model.cols) : '';
        },
      };
      findScanRef.current = startFindScan(
        access,
        query,
        { caseSensitive: findCaseSensitiveRef.current },
        (progress) => {
          findMatchesRef.current = progress;
          setFindUi((ui) => ({ ...ui, matchCount: progress.length }));
          renderSurface(true);
        },
        (matches) => {
          findScanRef.current = null;
          findMatchesRef.current = matches;
          const focused = initialFocusedMatch(matches);
          findFocusedIndexRef.current = focused;
          setFindUi((ui) => ({ ...ui, scanning: false, matchCount: matches.length, focusedIndex: focused }));
          if (focused >= 0) scrollToFindMatch(matches[focused]);
          renderSurface(true);
        },
      );
    }, [renderSurface, scrollToFindMatch, selectionLineAtBufferRow]);
    runFindScanRef.current = runFindScan;

    const findNavigate = useCallback((direction: 1 | -1) => {
      const matches = findMatchesRef.current;
      if (matches.length === 0) return;
      const current = findFocusedIndexRef.current;
      const next = current < 0
        ? matches.length - 1
        : (current + direction + matches.length) % matches.length;
      findFocusedIndexRef.current = next;
      setFindUi((ui) => ({ ...ui, focusedIndex: next }));
      scrollToFindMatch(matches[next]);
      renderSurface(true);
    }, [renderSurface, scrollToFindMatch]);

    const openFind = useCallback(() => {
      findOpenRef.current = true;
      setFindUi((ui) => ({ ...ui, open: true }));
      requestAnimationFrame(() => {
        const input = findInputRef.current;
        // Leave the caret alone if the user already typed: select() would replace it.
        if (!input || document.activeElement === input) return;
        input.focus();
        input.select();
      });
      if (findQueryRef.current) runFindScan();
    }, [runFindScan]);

    const blockRowAccess = useCallback((): BlockRowAccess | null => {
      const terminal = terminalRef.current;
      if (!terminal) return null;
      return {
        totalRows: () => terminal.getScrollbackLength() + terminal.rows,
        rowText: (row) => selectionLineAtBufferRow(row, 0, terminal.cols),
      };
    }, [selectionLineAtBufferRow]);

    const selectedBlock = useCallback((): TerminalBlock | null => {
      if (selectedBlockIdRef.current === null) return null;
      return blockStoreRef.current.blockById(selectedBlockIdRef.current);
    }, []);

    const getBlockState = useCallback((): BlockStateSnapshot => {
      const terminal = terminalRef.current;
      const access = blockRowAccess();
      if (!terminal || !access) return { ...EMPTY_BLOCK_STATE };
      const scrollback = terminal.getScrollbackLength();
      const viewportOffset = viewportOffsetRef.current;
      const firstViewportBufferRow = viewportBufferStart(scrollback, viewportOffset);
      const blocks: BlockStateSnapshotBlock[] = blockStoreRef.current.blocks().map((block) => ({
        id: block.id,
        command: block.command,
        exitCode: block.exitCode,
        promptRow: block.promptRow,
        outputStartRow: block.outputStartRow,
        endRow: block.endRow,
        anchorRow: block.anchorRow,
        anchorText: block.anchorText,
        reanchorDelta: reanchorDelta(block, access),
        viewportSpan: blockViewportSpanAnchored(block, access, firstViewportBufferRow, terminal.rows),
      }));
      return {
        cols: terminal.cols,
        rows: terminal.rows,
        scrollback,
        viewportOffset,
        firstViewportBufferRow,
        selectedBlockId: selectedBlockIdRef.current,
        blocks,
      };
    }, [blockRowAccess]);

    const selectBlock = useCallback((blockId: number | null) => {
      selectedBlockIdRef.current = blockId;
      renderSurface(true);
    }, [renderSurface]);

    const selectedBlockCopyText = useCallback((whole: boolean): string | null => {
      const block = selectedBlock();
      const access = blockRowAccess();
      if (!block || !access) return null;
      if (!whole) return block.command || null;
      const extracted = extractBlock(block, access);
      if (!extracted) return null;
      return extracted.output ? `${extracted.command}\n${extracted.output}` : extracted.command;
    }, [blockRowAccess, selectedBlock]);

    const copySelectedBlock = useCallback((whole: boolean): boolean => {
      const text = selectedBlockCopyText(whole);
      if (!text) return false;
      void writeClipboardText(text);
      return true;
    }, [selectedBlockCopyText]);

    const blockOutputText = useCallback((blockId: number): string | null => {
      const block = blockStoreRef.current.blockById(blockId);
      const access = blockRowAccess();
      if (!block || !access) return null;
      return extractBlock(block, access)?.output ?? null;
    }, [blockRowAccess]);

    const scrollToBufferRow = useCallback((bufferRow: number) => {
      const terminal = terminalRef.current;
      if (!terminal) return;
      const history = terminal.getScrollbackLength();
      viewportOffsetRef.current = Math.max(0, Math.min(history, history - bufferRow));
      wheelRemainderRowsRef.current = 0;
      hoverGenerationRef.current += 1;
      renderSurface(true);
    }, [renderSurface]);

    const scrollToBlockEdge = useCallback((blockId: number, edge: 'top' | 'bottom') => {
      const terminal = terminalRef.current;
      const block = blockStoreRef.current.blockById(blockId);
      if (!terminal || !block || block.endRow === undefined) return;
      const access = blockRowAccess();
      const delta = access ? reanchorDelta(block, access) ?? 0 : 0;
      const target = edge === 'top'
        ? block.promptRow + delta
        : Math.max(block.promptRow + delta, block.endRow + delta - terminal.rows);
      scrollToBufferRow(target);
    }, [blockRowAccess, scrollToBufferRow]);

    const pasteFromClipboard = useCallback(async () => {
      let text = '';
      try {
        text = await readClipboardText();
      } catch {
        return;
      }
      if (!text) return;
      const terminal = terminalRef.current;
      if (!terminal) return;
      onInputRef.current(terminal.formatPaste(text), 'user');
    }, []);

    const runBlockFilter = useCallback((blockId: number | null, caseSensitive: boolean) => {
      const query = filterQueryRef.current;
      const output = blockId !== null && query ? blockOutputText(blockId) : null;
      setFilterMatches(output ? filterBlockOutputLines(output, query, caseSensitive) : []);
    }, [blockOutputText]);

    const openBlockFilter = useCallback((blockId: number) => {
      filterQueryRef.current = '';
      setFilterMatches([]);
      setFilterUi({ open: true, blockId, caseSensitive: false });
      requestAnimationFrame(() => filterInputRef.current?.focus());
    }, []);

    const closeBlockFilter = useCallback(() => {
      if (filterRescanTimerRef.current) {
        clearTimeout(filterRescanTimerRef.current);
        filterRescanTimerRef.current = null;
      }
      filterQueryRef.current = '';
      setFilterMatches([]);
      setFilterUi({ open: false, blockId: null, caseSensitive: false });
      containerRef.current?.focus();
    }, []);

    const closeFind = useCallback(() => {
      findOpenRef.current = false;
      findScanRef.current?.cancel();
      findScanRef.current = null;
      if (findRescanTimerRef.current) {
        clearTimeout(findRescanTimerRef.current);
        findRescanTimerRef.current = null;
      }
      findMatchesRef.current = [];
      findFocusedIndexRef.current = -1;
      setFindUi((ui) => ({ ...ui, open: false, matchCount: 0, focusedIndex: -1, scanning: false }));
      renderSurface(true);
      containerRef.current?.focus();
    }, [renderSurface]);

    const enqueueOperation = useCallback((label: string, operation: () => void | Promise<void>) => {
      writeChainRef.current = writeChainRef.current
        .catch(() => undefined)
        .then(async () => {
          try {
            await operation();
          } catch (reason) {
            recoverFromModelFault(label, reason);
          }
        });
      return writeChainRef.current;
    }, [recoverFromModelFault]);

    const scheduleCoalescedRefit = useCallback(() => {
      if (overflowRefitRafRef.current !== null) {
        cancelAnimationFrame(overflowRefitRafRef.current);
      }
      overflowRefitRafRef.current = requestAnimationFrame(() => {
        overflowRefitRafRef.current = null;
        fitRef.current();
      });
    }, []);

    const write = useCallback((
      data: string | Uint8Array,
      options?: {
        suppressResponses?: boolean;
        deferRender?: boolean;
      },
    ) => {
      return enqueueOperation('write', async () => {
        const terminal = terminalRef.current;
        if (!terminal) return;
        const searchableOutput = typeof data === 'string' ? data : new TextDecoder().decode(data);
        if (searchableOutput) {
          const parsed = parseOsc52Writes(osc52StateRef.current, searchableOutput);
          osc52StateRef.current = parsed.state;
          for (const payload of parsed.payloads) {
            try {
              const bytes = Uint8Array.from(atob(payload), (char) => char.charCodeAt(0));
              void writeClipboardText(new TextDecoder().decode(bytes));
            } catch {
            }
          }
        }
        const scrollbackBefore = terminal.getScrollbackLength();
        const viewportOffsetBefore = viewportOffsetRef.current;
        const chunkBytes = typeof data === 'string' ? utf8Encoder.encode(data) : data;
        modelOpRingRef.current.noteWrite(chunkBytes);
        const osc133 = parseOsc133(osc133StateRef.current, chunkBytes);
        osc133StateRef.current = osc133.state;
        for (const segment of osc133.segments) {
          if (segment.bytes.length > 0) {
            // Re-assert grapheme clustering right after any RIS, or emoji later in this chunk break.
            graphemeResetCarryRef.current = writeReassertingClustering(
              terminal,
              segment.bytes,
              graphemeResetCarryRef.current,
            );
          }
          if (segment.marker) {
            const cursor = terminal.getCursor();
            blockStoreRef.current.applyMarker(
              segment.marker,
              { row: terminal.getScrollbackLength() + cursor.y, col: cursor.x },
              (row) => selectionLineAtBufferRow(row, 0, terminal.cols),
            );
          }
        }
        // Backstop for a DECRST 2027l this chunk did not split on.
        ensureGraphemeClustering(terminal);
        const responses: string[] = [];
        while (terminal.hasResponse()) {
          const response = terminal.readResponse();
          if (response) responses.push(response);
        }
        if (!options?.suppressResponses) {
          for (const response of responses) {
            // CPR, DA1, and OSC 10/11/12 replies are the worker's; answering here too
            // double-replies and the shell reads the extra bytes as stray input.
            const forwarded = stripDaemonOwnedResponses(response);
            if (forwarded) onInputRef.current(forwarded, 'response');
          }
        }
        viewportOffsetRef.current = offsetAfterWrite(
          viewportOffsetBefore,
          scrollbackBefore,
          terminal.getScrollbackLength(),
        );
        hoverGenerationRef.current += 1;
        annotationsRef.current?.noteWrite();
        if (findOpenRef.current && findQueryRef.current) {
          if (findRescanTimerRef.current) clearTimeout(findRescanTimerRef.current);
          findRescanTimerRef.current = setTimeout(() => {
            findRescanTimerRef.current = null;
            runFindScanRef.current?.();
          }, 300);
        }
        if (viewportOffsetRef.current === 0) {
          wheelRemainderRowsRef.current = 0;
        }
        const applicationSelectionAnchor = applicationSelectionAnchorRef.current;
        if (applicationSelectionAnchor) {
          const visibleLines = Array.from({ length: terminal.rows }, (_, row) => lineAtVisibleRow(row));
          selectionRef.current = relocateApplicationSelection(
            applicationSelectionAnchor,
            visibleLines,
            viewportBufferStart(terminal.getScrollbackLength(), viewportOffsetRef.current),
            terminal.cols,
          );
        }
        writeCountRef.current += 1;
        lastWriteAtRef.current = Date.now();
        const synchronizedOutput = parseSynchronizedOutput(
          synchronizedOutputStateRef.current,
          searchableOutput,
        );
        synchronizedOutputStateRef.current = synchronizedOutput.state;
        recordDiag({
          kind: 'write',
          pane: diagKeyRef.current,
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          model: modelInstanceRef.current,
          len: searchableOutput.length,
          syncActive: synchronizedOutput.state.active,
          shouldRender: synchronizedOutput.shouldRender,
          cols: terminal.cols,
          rows: terminal.rows,
        });
        if (options?.deferRender && synchronizedOutput.shouldRender) {
          return;
        }
        if (synchronizedOutput.shouldRender) {
          flushSynchronizedOutputRender();
        } else {
          scheduleSynchronizedOutputRenderFallback();
        }
      });
    }, [enqueueOperation, flushSynchronizedOutputRender, lineAtVisibleRow, scheduleCoalescedRefit, scheduleSynchronizedOutputRenderFallback, selectionLineAtBufferRow]);

    const restoreSnapshot = useCallback((snapshot: Uint8Array) => {
      return enqueueOperation('restoreSnapshot', () => {
        const terminal = terminalRef.current;
        if (!terminal) return;
        modelOpRingRef.current.noteRestoreChunk(snapshot, terminal.cols, terminal.rows);
        let historyDecoder: SnapshotHistoryDecoder;
        try {
          historyDecoder = terminal.adoptSnapshot(snapshot);
        } catch (reason) {
          // A payload fault, not a model fault: replacing the model would reattach and be
          // served the same bytes forever.
          recordUiDiag({
            kind: 'snapshot_decode_rejected',
            diagnosticFile: UI_DIAGNOSTICS_FILE,
            pane: diagKeyRef.current,
            session: runtimeMetaRef.current?.sessionId ?? undefined,
            bytes: snapshot.length,
            error: reason instanceof Error ? reason.message : String(reason),
          });
          return;
        }
        // The decoded terminal carries the worker's modes, and the worker never asserted grapheme clustering.
        graphemeResetCarryRef.current = false;
        ensureGraphemeClustering(terminal);
        osc133StateRef.current = emptyOsc133State();
        osc52StateRef.current = { pending: '' };
        viewportOffsetRef.current = 0;
        wheelRemainderRowsRef.current = 0;
        hoverGenerationRef.current += 1;
        annotationsRef.current?.noteWrite();
        flushSynchronizedOutputRender();
        requestAnimationFrame(() => {
          void enqueueOperation('restoreSnapshotHistory', () => {
            if (!terminalRef.current) {
              historyDecoder.close();
              return;
            }
            let restoredRows = 0;
            try {
              for (;;) {
                const rows = historyDecoder.decodeNextPage();
                if (rows === null) break;
                restoredRows += rows;
              }
            } catch (reason) {
              historyDecoder.close();
              recordUiDiag({
                kind: 'snapshot_history_decode_rejected',
                diagnosticFile: UI_DIAGNOSTICS_FILE,
                pane: diagKeyRef.current,
                session: runtimeMetaRef.current?.sessionId ?? undefined,
                bytes: snapshot.length,
                declaredHistoryRows: historyDecoder.declaredRows,
                restoredRows,
                error: reason instanceof Error ? reason.message : String(reason),
              });
            }
            flushSynchronizedOutputRender();
          });
        });
      });
    }, [enqueueOperation, flushSynchronizedOutputRender]);

    const seedBlocks = useCallback((blocks: SeededBlock[]) => {
      return enqueueOperation('seedBlocks', () => {
        const terminal = terminalRef.current;
        if (!terminal) return;
        blockStoreRef.current.seed(
          blocks,
          (row) => selectionLineAtBufferRow(row, 0, terminal.cols),
        );
        selectedBlockIdRef.current = null;
      });
    }, [enqueueOperation, selectionLineAtBufferRow]);

    const requestPlacementBlobs = useCallback(() => {
      const sessionId = placementSessionRef.current;
      if (!sessionId) return;
      for (const placement of placementStoreRef.current.placements()) {
        kittyImageCache.ensure(sessionId, placement.imageId, placement.generation);
      }
    }, []);

    const applyPlacements = useCallback((
      sessionId: string,
      seq: number,
      placements: PlacementElement[],
    ) => {
      return enqueueOperation('applyPlacements', () => {
        const terminal = terminalRef.current;
        if (!terminal) return;
        placementSessionRef.current = sessionId;
        if (!placementStoreRef.current.apply(seq, placements, terminal.getScrollbackLength())) {
          return;
        }
        requestPlacementBlobs();
        scheduleOutputRender(true);
      });
    }, [enqueueOperation, requestPlacementBlobs, scheduleOutputRender]);

    const seedPlacements = useCallback((sessionId: string, placements: PlacementElement[]) => {
      return enqueueOperation('seedPlacements', () => {
        const terminal = terminalRef.current;
        if (!terminal) return;
        placementSessionRef.current = sessionId;
        placementStoreRef.current.seed(placements, terminal.getScrollbackLength());
        requestPlacementBlobs();
        scheduleOutputRender(true);
      });
    }, [enqueueOperation, requestPlacementBlobs, scheduleOutputRender]);

    useEffect(() => kittyImageCache.subscribe((sessionId, imageId) => {
      if (sessionId !== placementSessionRef.current) return;
      if (!placementStoreRef.current.placements().some((p) => p.imageId === imageId)) return;
      scheduleOutputRender(true);
    }), [scheduleOutputRender]);

    const getPlacementState = useCallback((): PlacementStateSnapshot => {
      const terminal = terminalRef.current;
      if (!terminal) return { ...EMPTY_PLACEMENT_STATE };
      const renderer = rendererRef.current;
      const scrollback = terminal.getScrollbackLength();
      const viewportOffset = viewportOffsetRef.current;
      const firstViewportBufferRow = viewportBufferStart(scrollback, viewportOffset);
      const sessionId = placementSessionRef.current;
      const placements = placementStoreRef.current.placements().map((placement) => {
        const quad = renderer
          ? placementQuad(
            placement,
            firstViewportBufferRow,
            renderer.cellWidth,
            renderer.cellHeight,
            terminal.cols * renderer.cellWidth,
            terminal.rows * renderer.cellHeight,
            renderer.dpr,
          )
          : null;
        return {
          imageId: placement.imageId,
          placementId: placement.placementId,
          generation: placement.generation,
          z: placement.z,
          bufferRow: placement.bufferRow,
          col: placement.col,
          pixelWidth: placement.pixelWidth,
          pixelHeight: placement.pixelHeight,
          sourceX: placement.sourceX,
          sourceY: placement.sourceY,
          sourceWidth: placement.sourceWidth,
          sourceHeight: placement.sourceHeight,
          screenRow: placement.bufferRow - firstViewportBufferRow,
          visible: quad !== null,
          blob: sessionId
            ? kittyImageCache.status(sessionId, placement.imageId, placement.generation)
            : 'absent' as KittyImageStatus,
        };
      });
      return {
        sessionId,
        cols: terminal.cols,
        rows: terminal.rows,
        scrollback,
        viewportOffset,
        firstViewportBufferRow,
        lastAppliedSeq: placementStoreRef.current.lastAppliedSeq(),
        placements,
      };
    }, []);

    const reconcileBlocksAfterResize = useCallback((widthChanged: boolean) => {
      if (widthChanged) {
        annotationsRef.current?.noteGeometryChange();
        blockStoreRef.current.clear();
        placementStoreRef.current.clear();
        selectedBlockIdRef.current = null;
        return;
      }
      const access = blockRowAccess();
      if (!access) return;
      if (blockStoreRef.current.reanchorOnResize(access) === 'all-stale') {
        selectedBlockIdRef.current = null;
      }
    }, [blockRowAccess]);

    const resizeLocal = useCallback((
      cols: number,
      rows: number,
      options?: { restore?: boolean },
    ) => {
      return enqueueOperation('resizeLocal', () => {
        const terminal = terminalRef.current;
        const renderer = rendererRef.current;
        if (!terminal || !renderer) return;
        const fromCols = terminal.cols;
        const fromRows = terminal.rows;
        if (fromCols === cols && fromRows === rows) {
          modelSizeRef.current = { cols, rows };
          noteResize(diagKeyRef.current, {
            session: runtimeMetaRef.current?.sessionId ?? undefined,
            paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
            source: 'resizeLocal', fromCols, fromRows, toCols: cols, toRows: rows,
            noop: true,
          });
          return;
        }
        // No reflow, whichever call site: the worker does not reflow either, and the wire's
        // placements and blocks are numbered in its rows.
        modelOpRingRef.current.noteResize(cols, rows, true);
        resizeGhosttyWithoutReflow(terminal, cols, rows);
        reconcileBlocksAfterResize(cols !== fromCols);
        modelSizeRef.current = { cols, rows };
        renderer.resize(cols, rows);
        hoverGenerationRef.current += 1;
        noteResize(diagKeyRef.current, {
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          source: 'resizeLocal', fromCols, fromRows, toCols: cols, toRows: rows,
          noop: false,
          restore: options?.restore ?? false,
        });
        if (!options?.restore) {
          renderSurface(true);
          // The daemon's PTY rows are not bounded by this window; re-assert this client's
          // own floored geometry when they overflow the container.
          const container = containerRef.current;
          if (
            container
            && geometryOverflowsContainer(rows, renderer.cellHeight, container.clientHeight)
          ) {
            scheduleCoalescedRefit();
          }
        }
      });
    }, [enqueueOperation, reconcileBlocksAfterResize, renderSurface, scheduleCoalescedRefit]);

    const applyFitDimensions = useCallback((dims: TerminalDimensions) => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      if (!terminal || !renderer) {
        noteResize(diagKeyRef.current, {
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          source: 'fit',
          bail: renderer ? 'noModel' : 'noRenderer',
        });
        return;
      }
      // Inactive wrappers use display:none; resizing from that hidden geometry discards
      // an idle alternate-screen frame.
      const paneKind = runtimeMetaRef.current?.paneKind ?? undefined;
      const session = runtimeMetaRef.current?.sessionId ?? undefined;
      const fitContainer = containerRef.current;
      const cw = fitContainer?.clientWidth;
      const ch = fitContainer?.clientHeight;
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'inactiveSession', cw, ch });
        return;
      }
      if (!fitRequiresTerminalResize({ cols: terminal.cols, rows: terminal.rows }, dims)) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'sameSize', toCols: dims.cols, toRows: dims.rows, cw, ch, fromCols: terminal.cols, fromRows: terminal.rows });
        renderSurface(false);
        return;
      }
      try {
        const fromCols = terminal.cols;
        const fromRows = terminal.rows;
        modelOpRingRef.current.noteResize(dims.cols, dims.rows, true);
        resizeGhosttyWithoutReflow(terminal, dims.cols, dims.rows);
        reconcileBlocksAfterResize(dims.cols !== fromCols);
        modelSizeRef.current = dims;
        renderer.resize(dims.cols, dims.rows);
        hoverGenerationRef.current += 1;
        noteResize(diagKeyRef.current, {
          session, paneKind, source: 'fit', fromCols, fromRows, toCols: dims.cols, toRows: dims.rows,
        });
        if (!renderSurface(true)) return;
        // Cell metrics are CSS pixels; the PTY speaks device pixels.
        onResizeRef.current(dims.cols, dims.rows, {
          reason: 'ghostty_fit',
          xpixel: Math.round(dims.cols * renderer.cellWidth * renderer.dpr),
          ypixel: Math.round(dims.rows * renderer.cellHeight * renderer.dpr),
        });
      } catch (reason) {
        recoverFromModelFault('fit', reason);
      }
    }, [reconcileBlocksAfterResize, recoverFromModelFault, renderSurface]);

    applyFitDimensionsRef.current = applyFitDimensions;

    const fit = useCallback(() => {
      const container = containerRef.current;
      const renderer = rendererRef.current;
      if (!container || !renderer) {
        noteResize(diagKeyRef.current, {
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          source: 'fit',
          bail: renderer ? 'noContainer' : 'noRenderer',
        });
        return;
      }
      const paneKind = runtimeMetaRef.current?.paneKind ?? undefined;
      const session = runtimeMetaRef.current?.sessionId ?? undefined;
      if (runtimeMetaRef.current && !runtimeMetaRef.current.isActiveSession) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'inactiveSession', cw: container.clientWidth, ch: container.clientHeight });
        return;
      }
      const dims = renderer.fitDimensions(container.clientWidth, container.clientHeight);
      if (fitShouldBailAsSuspicious(
        runtimeMetaRef.current?.paneKind,
        dims,
        terminalRef.current?.cols ?? 0,
        terminalRef.current?.rows ?? 0,
        renderer.cellWidth,
        renderer.cellHeight,
        container.clientWidth,
        container.clientHeight,
      )) {
        noteResize(diagKeyRef.current, { session, paneKind, source: 'fit', bail: 'suspiciousSize', toCols: dims.cols, toRows: dims.rows, cw: container.clientWidth, ch: container.clientHeight });
        return;
      }
      hasMeasuredSizeRef.current = true;
      if (!fitResizeCoalescerRef.current) {
        fitResizeCoalescerRef.current = createResizeCoalescer(
          (dimensions) => applyFitDimensionsRef.current(dimensions),
        );
      }
      fitResizeCoalescerRef.current.submit(dims, isWorkspaceResizeDragActive(container));
    }, []);

    fitRef.current = fit;

    useImperativeHandle(ref, () => ({
      fit,
      openFind,
      focus: () => {
        if (filterInputRef.current) {
          if (document.activeElement !== filterInputRef.current) filterInputRef.current.focus();
          return true;
        }
        if (findOpenRef.current && findInputRef.current) {
          if (document.activeElement !== findInputRef.current) findInputRef.current.focus();
          return true;
        }
        const container = containerRef.current;
        if (!container) return false;
        container.focus();
        return document.activeElement === container;
      },
      typeTextViaInput: (text: string) => { onInputRef.current(text.replace(/\n/g, '\r')); return true; },
      isInputFocused: () => document.activeElement === containerRef.current,
      getBounds: () => containerRef.current?.getBoundingClientRect() ?? null,
      write,
      resizeLocal,
      restoreSnapshot,
      seedBlocks,
      applyPlacements,
      seedPlacements,
      reset: () => { recordDiag({ kind: 'reset', pane: diagKeyRef.current, session: runtimeMetaRef.current?.sessionId ?? undefined, model: modelInstanceRef.current }); modelOpRingRef.current.noteReset(); blockStoreRef.current.clear(); placementStoreRef.current.clear(); annotationsRef.current?.reset(); selectedBlockIdRef.current = null; void write('\x1bc'); },
      setSurfaceReleased,
      scrollToTop: () => {
        const terminal = terminalRef.current;
        if (!terminal) return false;
        viewportOffsetRef.current = terminal.getScrollbackLength();
        wheelRemainderRowsRef.current = 0;
        hoverGenerationRef.current += 1;
        renderSurface(true);
        return true;
      },
      getText,
      getSize: () => terminalRef.current ? { cols: terminalRef.current.cols, rows: terminalRef.current.rows } : null,
      hasMeasuredSize: () => hasMeasuredSizeRef.current,
      overflowsContainer: () => {
        const model = terminalRef.current;
        const renderer = rendererRef.current;
        const container = containerRef.current;
        if (!model || !renderer || !container) return false;
        return geometryOverflowsContainer(model.rows, renderer.cellHeight, container.clientHeight)
          || geometryOverflowsContainer(model.cols, renderer.cellWidth, container.clientWidth);
      },
      getVisibleContent,
      getVisibleStyleSummary,
      getBlockState,
      getPlacementState,
      drain: () => writeChainRef.current,
    }), [applyPlacements, fit, getBlockState, getPlacementState, getText, getVisibleContent, getVisibleStyleSummary, openFind, renderSurface, resizeLocal, restoreSnapshot, seedBlocks, seedPlacements, setSurfaceReleased, write]);

    useEffect(() => {
      let active = true;
      const container = containerRef.current;
      const canvas = canvasRef.current;
      if (!container || !canvas) return;
      const perfId = `ghostty-${debugNameRef.current}`;
      const resources = createRendererEffectResources();

      const scheduleRecovery = () => {
        if (resources.isRecoveryScheduled()) return;
        recoveryAttemptRef.current += 1;
        const attempt = recoveryAttemptRef.current;
        const delay = recoveryDelayMs(attempt);
        const session = runtimeMetaRef.current?.sessionId ?? undefined;
        const paneKind = runtimeMetaRef.current?.paneKind ?? undefined;
        if (delay === null) {
          noteRecovery(diagKeyRef.current, { session, paneKind, attempt, outcome: 'giveUp' });
          setError('Ghostty WebGL context lost. Reopen the pane to rebuild the renderer.');
          return;
        }
        noteRecovery(diagKeyRef.current, { session, paneKind, attempt, outcome: 'scheduled', delayMs: delay });
        resources.scheduleRecovery(() => {
          if (!active) return;
          setRendererEpoch((value) => value + 1);
        }, delay);
      };
      void loadGhostty().then((ghostty) => {
        if (!active) return;
        const theme = getTerminalTheme(resolvedTheme);
        const initialSize = modelSizeRef.current;
        const terminal = ghostty.createTerminal(initialSize.cols, initialSize.rows, {
          scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
          fgColor: colorNumber(theme.foreground),
          bgColor: colorNumber(theme.background),
          cursorColor: colorNumber(theme.cursor),
          palette: getTerminalAnsiPalette(resolvedTheme),
        });
        enableGraphemeClustering(terminal);
        modelOpRingRef.current.beginEpoch(initialSize.cols, initialSize.rows);
        graphemeResetCarryRef.current = false;
        synchronizedOutputStateRef.current = { active: false, pending: '' };
        clearSynchronizedOutputRenderTimer();
        modelInstanceRef.current += 1;
        osc133StateRef.current = emptyOsc133State();
        blockStoreRef.current.clear();
        placementStoreRef.current.clear();
        annotationsRef.current?.reset();
        selectedBlockIdRef.current = null;
        findScanRef.current?.cancel();
        findScanRef.current = null;
        findOpenRef.current = false;
        findMatchesRef.current = [];
        findFocusedIndexRef.current = -1;
        setFindUi((ui) => ({ ...ui, open: false, matchCount: 0, focusedIndex: -1, scanning: false }));
        recordDiag({
          kind: 'pane_mount',
          pane: diagKeyRef.current,
          label: debugNameRef.current,
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          model: modelInstanceRef.current,
          cols: initialSize.cols,
          rows: initialSize.rows,
        });
        const renderer = new WebGlTerminalRenderer(canvas, fontSizeRef.current, FONT_FAMILY, {
          background: theme.background,
          foreground: theme.foreground,
          cursor: theme.cursor,
        });
        terminalRef.current = terminal;
        rendererRef.current = renderer;
        // A theme change or fault recovery rebuilds every mounted pane, so the released
        // surface state has to survive the rebuild.
        if (surfaceReleasedRef.current) renderer.releaseDrawingBuffer();
        const recoveryAttempt = recoveryAttemptRef.current;
        const recoveredModelFault = modelRecoveryPendingRef.current;
        recoveryAttemptRef.current = 0;
        setError(null);
        // The bundled Nerd Font can load after the first glyphs rasterize blank.
        void ensureTerminalIconFont(fontSizeRef.current).then(() => {
          if (!active || rendererRef.current !== renderer) return;
          renderer.invalidateGlyphCache();
          renderSurface(true);
        });
        registerRenderProbe(diagKeyRef.current, () => {
          const model = terminalRef.current;
          if (!model) return null;
          const activeRenderer = rendererRef.current;
          const activeContainer = containerRef.current;
          return {
            cols: model.cols,
            rows: model.rows,
            modelPrintable: countModelPrintable(model),
            lastPaintAt: lastRenderAtRef.current,
            lastPaintQuads: lastPaintQuadsRef.current,
            active: runtimeMetaRef.current ? runtimeMetaRef.current.isActiveSession : true,
            session: runtimeMetaRef.current?.sessionId ?? undefined,
            isActivePane: runtimeMetaRef.current?.isActivePane ?? null,
            hasMeasuredSize: hasMeasuredSizeRef.current,
            cellWidth: activeRenderer?.cellWidth ?? null,
            cellHeight: activeRenderer?.cellHeight ?? null,
            clientWidth: activeContainer?.clientWidth ?? null,
            clientHeight: activeContainer?.clientHeight ?? null,
          };
        }, () => {
          fitRef.current();
        });
        const interceptKey = createTerminalKeyInterceptor((data) => onInputRef.current(data, 'user'));
        inputRef.current = attachTerminalInput({
          element: container,
          terminal: () => terminalRef.current,
          send: (data) => onInputRef.current(data, 'user'),
          interceptKey: (event) => {
            const meta = runtimeMetaRef.current;
            if (event.type === 'keydown' && meta) {
              noteTerminalKeyEvent(event, {
                runtimeId: meta.runtimeId,
                sessionId: meta.sessionId,
                paneId: meta.paneId,
              });
            }
            return interceptKey(event);
          },
          onError: (operation, reason) => {
            recoverFromModelFault(`input.${operation}`, reason);
          },
        });
        fit();
        // fit() contains WASM traps and schedules a fresh epoch: do not mark this model
        // ready if its initial fit faulted.
        if (modelFaultDedupeRef.current?.rendererEpoch === rendererEpoch) return;
        if (recoveryAttempt > 0 || recoveredModelFault) {
          noteRecovery(diagKeyRef.current, {
            session: runtimeMetaRef.current?.sessionId ?? undefined,
            paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
            attempt: recoveryAttempt || 1,
            outcome: 'recovered',
          });
        }
        readyRef.current = true;
        startupRef.current.firstReadyAt = Date.now();
        startupRef.current.firstReadyCols = terminal.cols;
        startupRef.current.firstReadyRows = terminal.rows;
        resources.observeResize(container, fit);
        onReadyRef.current({
          fit,
          openFind,
          focus: () => {
            if (filterInputRef.current) {
              if (document.activeElement !== filterInputRef.current) filterInputRef.current.focus();
              return true;
            }
            if (findOpenRef.current && findInputRef.current) {
              if (document.activeElement !== findInputRef.current) findInputRef.current.focus();
              return true;
            }
            container.focus();
            return document.activeElement === container;
          },
          typeTextViaInput: (text) => { onInputRef.current(text.replace(/\n/g, '\r')); return true; },
          isInputFocused: () => document.activeElement === container,
          getBounds: () => container.getBoundingClientRect(),
          write,
          resizeLocal,
          restoreSnapshot,
          seedBlocks,
          applyPlacements,
          seedPlacements,
          reset: () => { recordDiag({ kind: 'reset', pane: diagKeyRef.current, session: runtimeMetaRef.current?.sessionId ?? undefined, model: modelInstanceRef.current }); modelOpRingRef.current.noteReset(); blockStoreRef.current.clear(); placementStoreRef.current.clear(); annotationsRef.current?.reset(); selectedBlockIdRef.current = null; void write('\x1bc'); },
          setSurfaceReleased,
          scrollToTop: () => { viewportOffsetRef.current = terminal.getScrollbackLength(); wheelRemainderRowsRef.current = 0; hoverGenerationRef.current += 1; renderSurface(true); return true; },
          getText,
          getSize: () => ({ cols: terminal.cols, rows: terminal.rows }),
          hasMeasuredSize: () => hasMeasuredSizeRef.current,
          overflowsContainer: () => geometryOverflowsContainer(terminal.rows, renderer.cellHeight, container.clientHeight)
            || geometryOverflowsContainer(terminal.cols, renderer.cellWidth, container.clientWidth),
          getVisibleContent,
          getVisibleStyleSummary,
          getBlockState,
          getPlacementState,
          drain: () => writeChainRef.current,
        });
        if (recoveredModelFault) {
          modelRecoveryPendingRef.current = false;
          onTerminalModelRecoveredRef.current?.();
        }
      }).catch((reason) => {
        if (!active) return;
        noteRecovery(diagKeyRef.current, {
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          attempt: recoveryAttemptRef.current,
          outcome: 'constructFailed',
          error: String(reason),
        });
        scheduleRecovery();
      });
      const handleContextLost = (event: Event) => {
        event.preventDefault();
        if (!active) return;
        noteRecovery(diagKeyRef.current, {
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          paneKind: runtimeMetaRef.current?.paneKind ?? undefined,
          attempt: recoveryAttemptRef.current,
          outcome: 'contextLost',
        });
        setError(null);
        scheduleRecovery();
      };
      canvas.addEventListener('webglcontextlost', handleContextLost);
      const unregister = registerTerminalPerfGetter(perfId, () => {
        const terminal = terminalRef.current;
        const meta = runtimeMetaRef.current;
        if (!terminal) return null;
        return {
          terminalName: debugNameRef.current,
          sessionId: meta?.sessionId ?? null,
          paneId: meta?.paneId ?? null,
          runtimeId: meta?.runtimeId ?? null,
          paneKind: meta?.paneKind ?? null,
          isActivePane: meta?.isActivePane ?? null,
          isActiveSession: meta?.isActiveSession ?? null,
          cols: terminal.cols,
          rows: terminal.rows,
          bufferLength: terminal.rows + terminal.getScrollbackLength(),
          baseY: terminal.getScrollbackLength(),
          viewportY: viewportOffsetRef.current,
          scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
          alternateScreen: terminal.isAlternateScreen(),
          mouseTracking: terminal.hasMouseTracking(),
          renderer: 'ghostty-webgl',
          visible: true,
          writeQueueChunks: 0,
          writeQueueBytes: 0,
          renderCount: renderCountRef.current,
          renderCpuTotalMs: renderCpuTotalMsRef.current,
          renderCpuMaxMs: renderCpuMaxMsRef.current,
          lastRenderCpuMs: lastRenderCpuMsRef.current,
          renderFullCount: renderFullCountRef.current,
          renderPartialCount: renderPartialCountRef.current,
          renderRowsPainted: renderRowsPaintedRef.current,
          renderSubmittedQuads: renderSubmittedQuadsRef.current,
          renderRetainedRowVertexBytes: renderRetainedRowVertexBytesRef.current,
          renderRetainedStagingBytes: renderRetainedStagingBytesRef.current,
          modelPrintable: lastModelPrintableRef.current,
          lastPaintQuads: lastPaintQuadsRef.current,
          scheduledRenderRequests: scheduledRenderRequestsRef.current,
          scheduledRenderCoalesced: scheduledRenderCoalescedRef.current,
          scheduledRenderDeferred: scheduledRenderDeferredRef.current,
          writeParsedCount: writeCountRef.current,
          lastRenderAt: lastRenderAtRef.current,
          lastWriteParsedAt: lastWriteAtRef.current,
          lastRenderRange: null,
          ready: readyRef.current,
          startup: startupRef.current,
          lastResize: null,
          dom: { container: null, surface: null, canvas: canvas ? { width: canvas.clientWidth, height: canvas.clientHeight } : null },
        };
      });
      return () => {
        active = false;
        resources.dispose();
        recordDiag({
          kind: 'pane_unmount',
          pane: diagKeyRef.current,
          label: debugNameRef.current,
          session: runtimeMetaRef.current?.sessionId ?? undefined,
          model: modelInstanceRef.current,
        });
        disposePaneDiagnostics(diagKeyRef.current);
        if (overflowRefitRafRef.current !== null) {
          cancelAnimationFrame(overflowRefitRafRef.current);
          overflowRefitRafRef.current = null;
        }
        canvas.removeEventListener('webglcontextlost', handleContextLost);
        unregister();
        clearSynchronizedOutputRenderTimer();
        cancelScheduledOutputRender();
        fitResizeCoalescerRef.current?.cancel();
        fitResizeCoalescerRef.current = null;
        try {
          inputRef.current?.();
          rendererRef.current?.dispose();
          terminalRef.current?.free();
        } catch (reason) {
          recordUiDiag({
            kind: 'ghostty_model_cleanup_fault',
            diagnosticFile: UI_DIAGNOSTICS_FILE,
            pane: diagKeyRef.current,
            session: runtimeMetaRef.current?.sessionId ?? undefined,
            error: reason instanceof Error ? reason.message : String(reason),
            stack: reason instanceof Error ? reason.stack : undefined,
          });
        }
        inputRef.current = null;
        rendererRef.current = null;
        terminalRef.current = null;
        modelOpRingRef.current.clear();
        // Release only when canvasRef no longer points at this node: a same-canvas rebuild
        // would hand the new renderer a dead context. WKWebView's live-context pool is small.
        if (canvasRef.current !== canvas) {
          canvas.getContext('webgl2')?.getExtension('WEBGL_lose_context')?.loseContext();
        }
      };
    // Ghostty cells hold resolved default RGB, so a theme change needs a fresh model.
    // fontSize is not a dep: rebuilding every pane exhausts WKWebView's context pool.
    }, [cancelScheduledOutputRender, clearSynchronizedOutputRenderTimer, fit, getText, getVisibleContent, getVisibleStyleSummary, openFind, renderSurface, rendererEpoch, resizeLocal, resolvedTheme, restoreSnapshot, setSurfaceReleased, write]);

    useEffect(() => {
      const renderer = rendererRef.current;
      if (!renderer) return;
      renderer.setFontSize(fontSize);
      renderer.resize(modelSizeRef.current.cols, modelSizeRef.current.rows);
      void ensureTerminalIconFont(fontSize).then(() => {
        if (rendererRef.current !== renderer) return;
        renderer.invalidateGlyphCache();
        renderSurface(true);
      });
      fit();
      renderSurface(true);
    }, [fontSize]);

    const cellFromPointer = (event: React.MouseEvent | React.WheelEvent | MouseEvent) => {
      const renderer = rendererRef.current;
      const rect = canvasRef.current?.getBoundingClientRect();
      if (!renderer || !rect) return null;
      if (
        event.clientX < rect.left
        || event.clientX >= rect.right
        || event.clientY < rect.top
        || event.clientY >= rect.bottom
      ) {
        return null;
      }
      return {
        row: Math.max(0, Math.min((terminalRef.current?.rows ?? 1) - 1, Math.floor((event.clientY - rect.top) / renderer.cellHeight))),
        col: Math.max(0, Math.min((terminalRef.current?.cols ?? 1), Math.floor((event.clientX - rect.left) / renderer.cellWidth))),
      };
    };

    const recordPointerHitTest = useCallback((
      eventName: string,
      event: React.MouseEvent | MouseEvent,
      extra: Record<string, unknown> = {},
    ) => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      const container = containerRef.current;
      const canvas = canvasRef.current;
      if (!terminal || !renderer || !container || !canvas) return;
      const containerRect = container.getBoundingClientRect();
      const canvasRect = canvas.getBoundingClientRect();
      const containerCell = cellFromRect(event, containerRect, renderer.cellWidth, renderer.cellHeight, terminal.rows, terminal.cols);
      const canvasCell = cellFromRect(event, canvasRect, renderer.cellWidth, renderer.cellHeight, terminal.rows, terminal.cols);
      const selectedText = selectedTextRef.current;
      const selection = selectionRef.current;
      const containerLine = containerCell ? lineAtVisibleRow(containerCell.row) : '';
      const canvasLine = canvasCell ? lineAtVisibleRow(canvasCell.row) : '';
      const containerUri = containerCell ? urlAtColumn(containerLine, containerCell.col)?.uri ?? null : null;
      const canvasUri = canvasCell ? urlAtColumn(canvasLine, canvasCell.col)?.uri ?? null : null;
      recordTerminalLinkHitTestEvent({
        event: eventName,
        debugName: debugNameRef.current,
        sessionId: runtimeMetaRef.current?.sessionId,
        paneId: runtimeMetaRef.current?.paneId,
        runtimeId: runtimeMetaRef.current?.runtimeId,
        details: {
          pointer: {
            clientX: event.clientX,
            clientY: event.clientY,
            offsetFromContainer: {
              x: event.clientX - containerRect.left,
              y: event.clientY - containerRect.top,
            },
            offsetFromCanvas: {
              x: event.clientX - canvasRect.left,
              y: event.clientY - canvasRect.top,
            },
            metaKey: event.metaKey,
            ctrlKey: event.ctrlKey,
            altKey: event.altKey,
            shiftKey: event.shiftKey,
            button: event.button,
            buttons: event.buttons,
          },
          cells: {
            container: containerCell,
            canvas: canvasCell,
          },
          detected: {
            containerUri,
            canvasUri,
            containerLinePreview: containerLine,
            canvasLinePreview: canvasLine,
          },
          selection: {
            selecting: selectingRef.current,
            dragThresholdMet: selectionDragThresholdMetRef.current,
            range: selection,
            selectedTextPreview: selectedText,
            selectedTextLength: selectedText?.length ?? 0,
          },
          terminal: {
            cols: terminal.cols,
            rows: terminal.rows,
            scrollbackLength: terminal.getScrollbackLength(),
            viewportOffset: viewportOffsetRef.current,
            alternateScreen: terminal.isAlternateScreen(),
            mouseTracking: terminal.hasMouseTracking(),
          },
          geometry: {
            containerRect: rectSnapshot(containerRect),
            canvasRect: rectSnapshot(canvasRect),
            containerClient: {
              width: container.clientWidth,
              height: container.clientHeight,
            },
            canvasClient: {
              width: canvas.clientWidth,
              height: canvas.clientHeight,
            },
            canvasBacking: {
              width: canvas.width,
              height: canvas.height,
            },
            devicePixelRatio: window.devicePixelRatio,
            cellWidth: renderer.cellWidth,
            cellHeight: renderer.cellHeight,
          },
          ...extra,
        },
      });
    }, [lineAtVisibleRow]);

    const mouseModifiers = (event: Pick<MouseEvent, 'shiftKey' | 'altKey' | 'ctrlKey'>) =>
      (event.shiftKey ? 4 : 0) + (event.altKey ? 8 : 0) + (event.ctrlKey ? 16 : 0);

    const mouseButton = (button: number) => {
      if (button === 1) return 1;
      if (button === 2) return 2;
      return 0;
    };

    const hyperlinkUriAtViewportCell = useCallback((row: number, col: number): string | null => {
      const terminal = terminalRef.current;
      if (!terminal) return null;
      const history = terminal.getScrollbackLength();
      const bufferRow = bufferRowFromViewportRow(row, history, viewportOffsetRef.current);
      return bufferRow >= history
        ? terminal.getHyperlinkUri(bufferRow - history, col)
        : terminal.getScrollbackHyperlinkUri(bufferRow, col);
    }, []);

    const hoverLinkAtCell = useCallback((cell: { row: number; col: number } | null): DetectedTerminalLink | null => {
      const hover = hoverLinkRef.current;
      if (!cell || !hover?.link || hover.generation !== hoverGenerationRef.current) return null;
      const index = logicalIndexForCell(hover.line, cell.row, cell.col);
      if (index !== null && index >= hover.link.startCol && index < hover.link.endCol) {
        return hover.link;
      }
      return null;
    }, []);

    const updateLinkCursor = useCallback((cell: { row: number; col: number } | null, acceleratorHeld: boolean) => {
      setLinkCursorActive(Boolean(hoverLinkAtCell(cell) && acceleratorHeld));
    }, [hoverLinkAtCell]);

    const syncAnnotationHover = useCallback((
      cell: { row: number; col: number } | null,
      altHeld: boolean,
    ) => {
      const next = altHeld ? annotationAtCell(cell) : null;
      if (next === annotationHoverRef.current) return;
      annotationHoverRef.current = next;
      setAnnotationCursorActive(next !== null);
      renderSurface(true);
    }, [annotationAtCell, renderSurface]);

    const cachedPathExists = useCallback((absolutePath: string): Promise<boolean> => {
      const cache = pathExistsCacheRef.current;
      const cached = cache.get(absolutePath);
      if (cached !== undefined) return Promise.resolve(cached);
      if (cache.size > 512) cache.clear();
      const pending = exists(absolutePath)
        .catch(() => false)
        .then((result) => {
          cache.set(absolutePath, result);
          return result;
        });
      cache.set(absolutePath, pending);
      return pending;
    }, []);

    const knownPathExists = useCallback((absolutePath: string): boolean | undefined => {
      const cached = pathExistsCacheRef.current.get(absolutePath);
      return typeof cached === 'boolean' ? cached : undefined;
    }, []);

    const ensureHomeDir = useCallback(async (): Promise<string | null> => {
      if (homeDirRef.current === undefined) {
        try {
          homeDirRef.current = await homeDir();
        } catch {
          homeDirRef.current = null;
        }
      }
      return homeDirRef.current;
    }, []);

    // A scrollback row exposes no wrap flag, so a completely full previous row is treated as wrapping.
    const isContinuationRow = useCallback((viewportRow: number): boolean => {
      const terminal = terminalRef.current;
      if (!terminal) return false;
      const history = terminal.getScrollbackLength();
      const bufferRow = bufferRowFromViewportRow(viewportRow, history, viewportOffsetRef.current);
      if (bufferRow <= 0) return false;
      if (bufferRow > history) return terminal.rowWrapsIntoNext(bufferRow - history - 1);
      return selectionLineAtBufferRow(bufferRow - 1, 0, terminal.cols).length === terminal.cols;
    }, [selectionLineAtBufferRow]);

    const refreshSeedOccurrences = useCallback(() => {
      const terminal = terminalRef.current;
      const renderer = rendererRef.current;
      const canvas = canvasRef.current;
      const frame = containerRef.current?.parentElement;
      if (!terminal || !renderer || !canvas || !frame) return;
      const generation = hoverGenerationRef.current;
      const seeds = gardenSeedsRef.current;
      const current = visibleSeedOccurrencesRef.current;
      if (current?.generation === generation && current.seeds === seeds) return;

      const occurrences: TerminalSeedOccurrence[] = [];
      if (seeds.length > 0) {
        const knownSeedIds = new Set(seeds.map((seed) => seed.id));
        for (let row = 0; row < terminal.rows;) {
          const logical = logicalLineAt(
            lineAtVisibleRow,
            isContinuationRow,
            row,
            terminal.cols,
            terminal.rows,
          );
          occurrences.push(...seedOccurrencesInLine(logical, knownSeedIds));
          row = logical.firstRow + Math.max(1, logical.rowCount);
        }
      }
      visibleSeedOccurrencesRef.current = { generation, seeds, occurrences };

      const segments = occurrences.flatMap(seedOccurrenceSegments);
      if (segments.length === 0) {
        setSeedMarkLayout((layout) => layout === null ? layout : null);
        syncSeedPreviewAfterRefreshRef.current?.();
        return;
      }
      const canvasRect = canvas.getBoundingClientRect();
      const frameRect = frame.getBoundingClientRect();
      const canvasLeft = canvasRect.left - frameRect.left;
      const canvasTop = canvasRect.top - frameRect.top;
      const signature = [
        canvasLeft,
        canvasTop,
        renderer.cellWidth,
        renderer.cellHeight,
        ...segments.map((segment) => (
          `${segment.seedId}:${segment.row}:${segment.startCol}:${segment.endCol}`
        )),
      ].join('|');
      setSeedMarkLayout((layout) => layout?.signature === signature ? layout : {
        signature,
        canvasLeft,
        canvasTop,
        cellWidth: renderer.cellWidth,
        cellHeight: renderer.cellHeight,
        segments,
      });
      syncSeedPreviewAfterRefreshRef.current?.();
    }, [isContinuationRow, lineAtVisibleRow]);

    useLayoutEffect(() => {
      refreshSeedOccurrencesRef.current = refreshSeedOccurrences;
      refreshSeedOccurrences();
      return () => {
        if (refreshSeedOccurrencesRef.current === refreshSeedOccurrences) {
          refreshSeedOccurrencesRef.current = null;
        }
      };
    }, [refreshSeedOccurrences]);

    useEffect(() => {
      visibleSeedOccurrencesRef.current = null;
      refreshSeedOccurrencesRef.current?.();
    }, [gardenSeeds]);

    const detectHoverLink = useCallback((
      cell: { row: number; col: number } | null,
      options: { force?: boolean; repaint?: boolean } = {},
    ) => {
      const force = options.force ?? false;
      const repaint = options.repaint ?? true;
      const generation = hoverGenerationRef.current;
      const current = hoverLinkRef.current;
      if (!force && cell && current && current.generation === generation) {
        const cachedIndex = logicalIndexForCell(current.line, cell.row, cell.col);
        if (cachedIndex !== null && cachedIndex >= current.startIndex && cachedIndex < current.endIndex) {
          return;
        }
      }
      const hadUnderline = Boolean(current?.link);
      const clearHover = () => {
        hoverLinkRef.current = null;
        setLinkCursorActive(false);
        if (hadUnderline) {
          if (repaint) renderSurface(true);
        }
      };
      const terminal = terminalRef.current;
      if (!cell || !terminal) {
        clearHover();
        return;
      }
      const logical = logicalLineAt(lineAtVisibleRow, isContinuationRow, cell.row, terminal.cols, terminal.rows);
      const index = logicalIndexForCell(logical, cell.row, cell.col);
      if (index === null) {
        clearHover();
        return;
      }
      // OSC 8 first: a hyperlink's visible label can contain spaces, which the fragment detectors would clip.
      const hyperlink = hyperlinkRangeAt(
        (i) => hyperlinkUriAtViewportCell(logical.firstRow + Math.floor(i / logical.cols), i % logical.cols),
        index,
        logical.text.length,
      );
      if (hyperlink) {
        hoverLinkRef.current = {
          generation,
          line: logical,
          startIndex: hyperlink.startCol,
          endIndex: hyperlink.endCol,
          link: { kind: 'url', uri: hyperlink.uri, startCol: hyperlink.startCol, endCol: hyperlink.endCol },
          linkSpan: spanFromLogicalRange(logical, hyperlink.startCol, hyperlink.endCol),
        };
        if (repaint) renderSurface(true);
        updateLinkCursor(cell, acceleratorHeldRef.current);
        return;
      }
      const url = urlAtColumn(logical.text, index);
      if (url) {
        hoverLinkRef.current = {
          generation,
          line: logical,
          startIndex: url.startCol,
          endIndex: url.endCol,
          link: { kind: 'url', uri: url.uri, startCol: url.startCol, endCol: url.endCol },
          linkSpan: spanFromLogicalRange(logical, url.startCol, url.endCol),
        };
        if (repaint) renderSurface(true);
        updateLinkCursor(cell, acceleratorHeldRef.current);
        return;
      }
      const fragment = fragmentAtColumn(logical.text, index);
      if (!fragment) {
        clearHover();
        return;
      }
      const entry: HoverLinkState = {
        generation,
        line: logical,
        startIndex: fragment.startCol,
        endIndex: fragment.endCol,
        link: null,
        linkSpan: null,
      };
      hoverLinkRef.current = entry;
      if (hadUnderline) {
        setLinkCursorActive(false);
        if (repaint) renderSurface(true);
      }
      const candidates = pathCandidatesForFragment(
        logical.text.slice(fragment.startCol, fragment.endCol),
        fragment.startCol,
      );
      if (candidates.length === 0) return;

      const previousPath = current?.link?.kind === 'path' ? current.link : null;
      for (const candidate of candidates) {
        const absolutePath = resolveDetectedPath(
          candidate.path,
          cwdRef.current,
          homeDirRef.current ?? undefined,
        );
        if (!absolutePath) continue;
        const reusesPrevious = Boolean(
          previousPath
          && previousPath.absolutePath === absolutePath
          && previousPath.startCol === candidate.startCol
          && previousPath.endCol === candidate.endCol,
        );
        if (!reusesPrevious && knownPathExists(absolutePath) !== true) continue;
        entry.link = {
          kind: 'path',
          absolutePath,
          line: candidate.line,
          column: candidate.column,
          startCol: candidate.startCol,
          endCol: candidate.endCol,
        };
        entry.linkSpan = spanFromLogicalRange(logical, candidate.startCol, candidate.endCol);
        if (repaint) renderSurface(true);
        updateLinkCursor(cell, acceleratorHeldRef.current);
        return;
      }

      void (async () => {
        const home = await ensureHomeDir();
        for (const candidate of candidates) {
          const absolutePath = resolveDetectedPath(candidate.path, cwdRef.current, home ?? undefined);
          if (!absolutePath) continue;
          const known = knownPathExists(absolutePath);
          if (known === false) continue;
          if (known !== true && !(await cachedPathExists(absolutePath))) continue;
          if (hoverLinkRef.current !== entry) return;
          if (hoverGenerationRef.current !== generation) {
            refreshHoverLinkRef.current?.();
            return;
          }
          entry.link = {
            kind: 'path',
            absolutePath,
            line: candidate.line,
            column: candidate.column,
            startCol: candidate.startCol,
            endCol: candidate.endCol,
          };
          entry.linkSpan = spanFromLogicalRange(logical, candidate.startCol, candidate.endCol);
          renderSurface(true);
          updateLinkCursor(hoveredCellRef.current, acceleratorHeldRef.current);
          return;
        }
      })();
    }, [cachedPathExists, ensureHomeDir, hyperlinkUriAtViewportCell, isContinuationRow, knownPathExists, lineAtVisibleRow, renderSurface, updateLinkCursor]);

    useLayoutEffect(() => {
      const refreshHoverLink = () => {
        const current = hoverLinkRef.current;
        if (!current || current.generation === hoverGenerationRef.current) return;
        detectHoverLink(hoveredCellRef.current, { force: true, repaint: false });
      };
      refreshHoverLinkRef.current = refreshHoverLink;
      return () => {
        if (refreshHoverLinkRef.current === refreshHoverLink) {
          refreshHoverLinkRef.current = null;
        }
      };
    }, [detectHoverLink]);

    const seedOccurrenceAt = useCallback((cell: { row: number; col: number } | null) => {
      refreshSeedOccurrencesRef.current?.();
      return seedOccurrenceAtCell(
        visibleSeedOccurrencesRef.current?.occurrences ?? [],
        cell,
      );
    }, []);

    const seedAnchorFor = useCallback((
      occurrence: TerminalSeedOccurrence,
      cell: { row: number; col: number },
    ): TerminalSeedAnchor | null => {
      const renderer = rendererRef.current;
      const canvasRect = canvasRef.current?.getBoundingClientRect();
      const frameRect = containerRef.current?.parentElement?.getBoundingClientRect();
      if (!renderer || !canvasRect || !frameRect) return null;
      const segment = seedOccurrenceSegments(occurrence).find((candidate) => (
        candidate.row === cell.row
        && cell.col >= candidate.startCol
        && cell.col < candidate.endCol
      ));
      if (!segment) return null;
      return {
        left: canvasRect.left + segment.startCol * renderer.cellWidth,
        right: canvasRect.left + segment.endCol * renderer.cellWidth,
        top: canvasRect.top + segment.row * renderer.cellHeight,
        bottom: canvasRect.top + (segment.row + 1) * renderer.cellHeight,
        bounds: {
          left: frameRect.left,
          right: frameRect.right,
          top: frameRect.top,
          bottom: frameRect.bottom,
        },
      };
    }, []);

    const closeSeedPreview = useCallback(() => {
      if (seedPreviewOpenTimerRef.current !== null) {
        window.clearTimeout(seedPreviewOpenTimerRef.current);
        seedPreviewOpenTimerRef.current = null;
      }
      if (seedPreviewCloseTimerRef.current !== null) {
        window.clearTimeout(seedPreviewCloseTimerRef.current);
        seedPreviewCloseTimerRef.current = null;
      }
      seedPreviewPendingIdRef.current = null;
      seedPreviewPointerInsideRef.current = false;
      seedPreviewRef.current = null;
      setSeedPreview((preview) => preview === null ? preview : null);
    }, []);

    const cancelSeedPreviewClose = useCallback(() => {
      if (seedPreviewCloseTimerRef.current === null) return;
      window.clearTimeout(seedPreviewCloseTimerRef.current);
      seedPreviewCloseTimerRef.current = null;
    }, []);

    const scheduleSeedPreviewClose = useCallback((delay = SEED_PREVIEW_CLOSE_DELAY_MS) => {
      if (seedPreviewOpenTimerRef.current !== null) {
        window.clearTimeout(seedPreviewOpenTimerRef.current);
        seedPreviewOpenTimerRef.current = null;
      }
      seedPreviewPendingIdRef.current = null;
      if (seedPreviewCloseTimerRef.current !== null) return;
      seedPreviewCloseTimerRef.current = window.setTimeout(() => {
        seedPreviewCloseTimerRef.current = null;
        if (seedPreviewPointerInsideRef.current) return;
        closeSeedPreview();
      }, delay);
    }, [closeSeedPreview]);

    const syncSeedPreview = useCallback((cell: { row: number; col: number } | null) => {
      const occurrence = seedOccurrenceAt(cell);
      const anchor = occurrence && cell ? seedAnchorFor(occurrence, cell) : null;
      if (!occurrence || !anchor) {
        scheduleSeedPreviewClose();
        return;
      }
      cancelSeedPreviewClose();
      const active = seedPreviewRef.current;
      if (active?.seedId === occurrence.seedId) {
        if (!sameSeedAnchor(active.anchor, anchor)) {
          const next = { seedId: occurrence.seedId, anchor };
          seedPreviewRef.current = next;
          setSeedPreview(next);
        }
        return;
      }
      if (seedPreviewPendingIdRef.current === occurrence.seedId) return;
      if (active) {
        seedPreviewRef.current = null;
        setSeedPreview(null);
      }
      if (seedPreviewOpenTimerRef.current !== null) {
        window.clearTimeout(seedPreviewOpenTimerRef.current);
      }
      seedPreviewPendingIdRef.current = occurrence.seedId;
      seedPreviewOpenTimerRef.current = window.setTimeout(() => {
        seedPreviewOpenTimerRef.current = null;
        seedPreviewPendingIdRef.current = null;
        refreshSeedOccurrencesRef.current?.();
        const hovered = hoveredCellRef.current;
        const current = seedOccurrenceAtCell(
          visibleSeedOccurrencesRef.current?.occurrences ?? [],
          hovered,
        );
        if (!hovered || current?.seedId !== occurrence.seedId) return;
        const currentAnchor = seedAnchorFor(current, hovered);
        if (!currentAnchor) return;
        const next = { seedId: current.seedId, anchor: currentAnchor };
        seedPreviewRef.current = next;
        setSeedPreview(next);
      }, SEED_PREVIEW_OPEN_DELAY_MS);
    }, [cancelSeedPreviewClose, scheduleSeedPreviewClose, seedAnchorFor, seedOccurrenceAt]);

    const syncSeedPreviewAfterRefresh = useCallback(() => {
      const active = seedPreviewRef.current;
      if (!active || seedPreviewPointerInsideRef.current) return;
      const hovered = hoveredCellRef.current;
      const occurrence = seedOccurrenceAtCell(
        visibleSeedOccurrencesRef.current?.occurrences ?? [],
        hovered,
      );
      if (!hovered || occurrence?.seedId !== active.seedId) {
        scheduleSeedPreviewClose();
        return;
      }
      const anchor = seedAnchorFor(occurrence, hovered);
      if (!anchor || sameSeedAnchor(active.anchor, anchor)) return;
      const next = { seedId: active.seedId, anchor };
      seedPreviewRef.current = next;
      setSeedPreview(next);
    }, [scheduleSeedPreviewClose, seedAnchorFor]);

    useLayoutEffect(() => {
      syncSeedPreviewAfterRefreshRef.current = syncSeedPreviewAfterRefresh;
      return () => {
        if (syncSeedPreviewAfterRefreshRef.current === syncSeedPreviewAfterRefresh) {
          syncSeedPreviewAfterRefreshRef.current = null;
        }
      };
    }, [syncSeedPreviewAfterRefresh]);

    useEffect(() => () => {
      if (seedPreviewOpenTimerRef.current !== null) {
        window.clearTimeout(seedPreviewOpenTimerRef.current);
      }
      if (seedPreviewCloseTimerRef.current !== null) {
        window.clearTimeout(seedPreviewCloseTimerRef.current);
      }
    }, []);

    const linkAtCell = useCallback((cell: { row: number; col: number } | null): DetectedTerminalLink | null => {
      refreshHoverLinkRef.current?.();
      const hovered = hoverLinkAtCell(cell);
      if (hovered) return hovered;
      if (!cell) return null;
      const terminal = terminalRef.current;
      if (terminal) {
        const hyperlink = hyperlinkRangeAt(
          (i) => hyperlinkUriAtViewportCell(cell.row, i),
          cell.col,
          terminal.cols,
        );
        if (hyperlink) return { kind: 'url', uri: hyperlink.uri, startCol: hyperlink.startCol, endCol: hyperlink.endCol };
      }
      const url = urlAtColumn(lineAtVisibleRow(cell.row), cell.col);
      return url ? { kind: 'url', uri: url.uri, startCol: url.startCol, endCol: url.endCol } : null;
    }, [hoverLinkAtCell, hyperlinkUriAtViewportCell, lineAtVisibleRow]);

    const openLink = useCallback((link: DetectedTerminalLink) => {
      const action = terminalLinkOpenAction(link);
      if (!action) return;
      switch (action.action) {
        case 'open-url':
          void openUrl(action.uri);
          break;
        case 'open-markdown':
          if (onOpenMarkdownRef.current) {
            onOpenMarkdownRef.current(action.path, runtimeMetaRef.current?.sessionId ?? '');
            break;
          }
          void openPath(action.path);
          break;
        case 'open-path':
          void openPath(action.path);
          break;
      }
    }, []);

    useEffect(() => {
      const handleModifierChange = (event: KeyboardEvent) => {
        if (event.key === 'Alt') {
          altHeldRef.current = event.altKey;
          syncAnnotationHover(hoveredCellRef.current, event.altKey);
          return;
        }
        if (event.key !== 'Meta' && event.key !== 'Control') return;
        acceleratorHeldRef.current = event.metaKey || event.ctrlKey;
        if (acceleratorHeldRef.current) detectHoverLink(hoveredCellRef.current);
        updateLinkCursor(hoveredCellRef.current, acceleratorHeldRef.current);
      };
      window.addEventListener('keydown', handleModifierChange);
      window.addEventListener('keyup', handleModifierChange);
      return () => {
        window.removeEventListener('keydown', handleModifierChange);
        window.removeEventListener('keyup', handleModifierChange);
      };
    }, [detectHoverLink, syncAnnotationHover, updateLinkCursor]);

    useEffect(() => () => {
      selectionDragCleanupRef.current?.();
      selectionDragCleanupRef.current = null;
      trackedMouseReleaseCleanupRef.current?.();
      trackedMouseReleaseCleanupRef.current = null;
      const terminal = terminalRef.current;
      const button = trackedMouseButtonRef.current;
      const cell = trackedMouseCellRef.current;
      if (terminal && button !== null && cell) {
        onInputRef.current(applicationMouseInput(
          'release',
          button,
          cell.col + 1,
          cell.row + 1,
          terminal.getMode(1006),
        ), 'pointer');
      }
      trackedMouseButtonRef.current = null;
      trackedMouseCellRef.current = null;
    }, []);

    const stopTrackedMouseReleaseWatch = () => {
      trackedMouseReleaseCleanupRef.current?.();
      trackedMouseReleaseCleanupRef.current = null;
    };

    const releaseTrackedMouse = (
      event?: React.MouseEvent | MouseEvent,
    ): boolean => {
      const terminal = terminalRef.current;
      const button = trackedMouseButtonRef.current;
      if (button === null) return false;
      const cell = (event ? cellFromPointer(event) : null) ?? trackedMouseCellRef.current;
      stopTrackedMouseReleaseWatch();
      trackedMouseButtonRef.current = null;
      trackedMouseCellRef.current = null;
      if (!terminal || !cell) return false;
      onInputRef.current(applicationMouseInput(
        'release',
        button,
        cell.col + 1,
        cell.row + 1,
        terminal.getMode(1006),
        event ? mouseModifiers(event) : 0,
      ), 'pointer');
      return true;
    };

    const startTrackedMouseReleaseWatch = () => {
      stopTrackedMouseReleaseWatch();
      const onUp = (event: MouseEvent) => {
        releaseTrackedMouse(event);
      };
      const onCancel = () => {
        releaseTrackedMouse();
      };
      document.addEventListener('mouseup', onUp);
      window.addEventListener('blur', onCancel);
      window.addEventListener('pointercancel', onCancel);
      trackedMouseReleaseCleanupRef.current = () => {
        document.removeEventListener('mouseup', onUp);
        window.removeEventListener('blur', onCancel);
        window.removeEventListener('pointercancel', onCancel);
      };
    };

    const sendTrackedMouse = (
      action: 'press' | 'move' | 'release',
      event: React.MouseEvent,
    ): boolean => {
      if (isWorkspaceResizeActive(containerRef.current)) return false;
      const terminal = terminalRef.current;
      if (action === 'release') {
        const released = releaseTrackedMouse(event);
        if (released) event.preventDefault();
        return released;
      }
      const cell = cellFromPointer(event);
      if (!terminal || !cell || !terminal.hasMouseTracking()) return false;
      const activeButton = trackedMouseButtonRef.current;
      if (action === 'move') {
        const shouldReport = shouldReportApplicationMouseMove({
          anyEventMouseTracking: terminal.getMode(1003),
          dragMouseTracking: terminal.getMode(1002),
          activeButton,
          buttons: event.buttons,
        });
        if (!shouldReport) {
          if (activeButton !== null && event.buttons === 0) {
            releaseTrackedMouse(event);
          }
          return true;
        }
      }
      // Under DECSET 1003 passive motion is button 3 + motion (35); button 0 + motion (32)
      // would tell the application a left-drag is active.
      const button = action === 'press' ? mouseButton(event.button) : activeButton ?? 3;
      onInputRef.current(applicationMouseInput(
        action,
        button,
        cell.col + 1,
        cell.row + 1,
        terminal.getMode(1006),
        mouseModifiers(event),
      ), 'pointer');
      trackedMouseCellRef.current = cell;
      if (action === 'press') {
        trackedMouseButtonRef.current = button;
        startTrackedMouseReleaseWatch();
      }
      event.preventDefault();
      return true;
    };

    const stopSelectionDrag = () => {
      selectionDragCleanupRef.current?.();
      selectionDragCleanupRef.current = null;
    };

    const finishSelectionDrag = async (event: MouseEvent) => {
      stopSelectionDrag();
      if (!selectingRef.current) return;
      selectingRef.current = false;
      selectionPointerStartRef.current = null;
      const wasClick = !selectionDragThresholdMetRef.current;
      const annotationDrag = annotationDragRef.current && !wasClick;
      const annotationClick = wasClick ? annotationClickRef.current : null;
      annotationDragRef.current = false;
      annotationClickRef.current = null;
      if (wasClick) {
        selectionRef.current = null;
        renderSurface(true);
      }
      if (annotationClick !== null) {
        onAnnotationActivateRef.current?.(annotationClick, {
          clientX: event.clientX,
          clientY: event.clientY,
        });
        return;
      }
      if (annotationDrag && selectionRef.current) {
        const store = annotationsRef.current;
        const access = messageRowAccess();
        const anchor = store && access
          ? store.anchorForSelection(access, normalizeSelection(selectionRef.current))
          : null;
        if (anchor) {
          selectionRef.current = null;
          selectedTextRef.current = null;
          renderSurface(true);
          onAnnotationAnchorRef.current?.(anchor, { clientX: event.clientX, clientY: event.clientY });
          return;
        }
        onAnnotationMissRef.current?.(
          store?.hasMessages() ? 'outside-messages' : 'no-messages',
          { clientX: event.clientX, clientY: event.clientY },
        );
      }
      const text = textForSelectionRange(selectionRef.current);
      selectedTextRef.current = text || null;
      if (text) await writeClipboardText(text);
      const cell = cellFromPointer(event);
      const link = linkAtCell(cell);
      recordPointerHitTest('mouseup', event, {
        activeCell: cell,
        activeUri: link ? link.uri ?? link.absolutePath ?? null : null,
        opensUri: Boolean(link && !text && (event.metaKey || event.ctrlKey)),
        copiedTextLength: text.length,
        phase: 'after-selection',
      });
      if (link && !text && (event.metaKey || event.ctrlKey)) {
        openLink(link);
        return;
      }
      if (wasClick && !text && !(event.metaKey || event.ctrlKey)) {
        const terminal = terminalRef.current;
        let nextBlockId: number | null = null;
        const access = blockRowAccess();
        if (terminal && cell && access && blockStoreRef.current.hasBlocks()) {
          const bufferRow = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          const block = blockStoreRef.current.blockAtAnchored(bufferRow, access);
          if (block) {
            nextBlockId = block.id;
            // blockAtAnchored matched at a possibly non-zero delta, so shift the stored rows by
            // it before comparing against live buffer rows.
            const delta = reanchorDelta(block, access) ?? 0;
            if (block.outputStartRow !== undefined && bufferRow < block.outputStartRow + delta && block.inputStart) {
              const lastCommandRow = block.outputStartRow - 1 + delta;
              const lineLength = selectionLineAtBufferRow(lastCommandRow, 0, terminal.cols).trimEnd().length;
              selectionRef.current = {
                startRow: block.inputStart.row + delta,
                startCol: block.inputStart.col,
                endRow: lastCommandRow,
                endCol: Math.max(lineLength, block.inputStart.col + 1),
              };
              selectedTextRef.current = block.command || null;
            }
          }
        }
        if (nextBlockId !== null || selectionRef.current) {
          selectBlock(nextBlockId);
        }
      }
    };

    const cancelSelectionDrag = () => {
      stopSelectionDrag();
      if (!selectingRef.current) return;
      selectingRef.current = false;
      selectionPointerStartRef.current = null;
      annotationDragRef.current = false;
      annotationClickRef.current = null;
      if (!selectionDragThresholdMetRef.current) {
        selectionRef.current = null;
        renderSurface(true);
        return;
      }
      const text = textForSelectionRange(selectionRef.current);
      selectedTextRef.current = text || null;
      if (text) void writeClipboardText(text);
    };

    const startSelectionDrag = () => {
      stopSelectionDrag();
      const onMove = (event: MouseEvent) => {
        if (!selectingRef.current || !selectionRef.current) return;
        // `buttons` can go stale in WebKit after a release outside the webview.
        if ((event.buttons & 1) === 0) {
          void finishSelectionDrag(event);
          return;
        }
        const terminal = terminalRef.current;
        const renderer = rendererRef.current;
        const cell = cellFromPointer(event);
        const pointerStart = selectionPointerStartRef.current;
        if (!terminal || !renderer || !cell || !pointerStart) return;
        if (!selectionDragThresholdMetRef.current) {
          const deltaX = event.clientX - pointerStart.clientX;
          const deltaY = event.clientY - pointerStart.clientY;
          const threshold = renderer.cellWidth * 0.5;
          if (deltaX * deltaX + deltaY * deltaY < threshold * threshold) return;
          selectionDragThresholdMetRef.current = true;
        }
        recordPointerHitTest('mousemove', event, {
          activeCell: cell,
          phase: 'selection-drag',
        });
        const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
        selectionRef.current = { ...selectionRef.current, endRow: row, endCol: cell.col + 1 };
        renderSurface(true);
      };
      const onUp = (event: MouseEvent) => {
        void finishSelectionDrag(event);
      };
      // Second net: a release outside the window, or one a descendant stopPropagation()s,
      // never reaches the document mouseup listener.
      const onCancel = () => {
        cancelSelectionDrag();
      };
      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
      window.addEventListener('blur', onCancel);
      window.addEventListener('pointercancel', onCancel);
      selectionDragCleanupRef.current = () => {
        document.removeEventListener('mousemove', onMove);
        document.removeEventListener('mouseup', onUp);
        window.removeEventListener('blur', onCancel);
        window.removeEventListener('pointercancel', onCancel);
      };
    };

    const contextMenuBlock = contextMenu?.blockId != null
      ? blockStoreRef.current.blockById(contextMenu.blockId)
      : null;
    const onMac = isMacLikePlatform();
    const clipboardCopyHint = onMac ? keyCombo('accel', 'C') : keyCombo('ctrl', 'shift', 'C');
    const clipboardPasteHint = onMac ? keyCombo('accel', 'V') : keyCombo('ctrl', 'shift', 'V');
    const clipboardCopyCommandHint = onMac ? keyCombo('shift', 'accel', 'C') : keyCombo('ctrl', 'alt', 'C');
    const contextMenuItems: TerminalContextMenuItem[] = contextMenu ? [
      { id: 'copy', label: 'Copy', shortcut: clipboardCopyHint, disabled: !selectedTextRef.current && !contextMenuBlock },
      { id: 'copy-command', label: 'Copy command', shortcut: clipboardCopyCommandHint, disabled: !contextMenuBlock?.command },
      { id: 'copy-output', label: 'Copy output', disabled: !contextMenuBlock },
      { id: 'paste', label: 'Paste', shortcut: clipboardPasteHint, separatorBefore: true },
      { id: 'filter-block', label: 'Filter block output', separatorBefore: true, disabled: !contextMenuBlock },
      { id: 'find', label: 'Find', shortcut: formatShortcut('terminal.find') },
      { id: 'scroll-block-top', label: 'Scroll to top of block', separatorBefore: true, disabled: !contextMenuBlock },
      { id: 'scroll-block-bottom', label: 'Scroll to bottom of block', disabled: !contextMenuBlock },
    ] : [];

    const handleContextMenuSelect = (id: string) => {
      const blockId = contextMenu?.blockId ?? null;
      setContextMenu(null);
      const refocusTerminal = () => containerRef.current?.focus();
      switch (id) {
        case 'copy': {
          const text = selectedTextRef.current ?? selectedBlockCopyText(true);
          if (text) void writeClipboardText(text);
          refocusTerminal();
          break;
        }
        case 'copy-command': {
          copySelectedBlock(false);
          refocusTerminal();
          break;
        }
        case 'copy-output': {
          const output = blockId !== null ? blockOutputText(blockId) : null;
          if (output) void writeClipboardText(output);
          refocusTerminal();
          break;
        }
        case 'paste': {
          void pasteFromClipboard();
          refocusTerminal();
          break;
        }
        case 'filter-block': {
          if (blockId !== null) openBlockFilter(blockId);
          break;
        }
        case 'find': {
          openFind();
          break;
        }
        case 'scroll-block-top': {
          if (blockId !== null) scrollToBlockEdge(blockId, 'top');
          refocusTerminal();
          break;
        }
        case 'scroll-block-bottom': {
          if (blockId !== null) scrollToBlockEdge(blockId, 'bottom');
          refocusTerminal();
          break;
        }
        default:
          break;
      }
    };

    const scrollToFilteredLine = (lineOffset: number) => {
      const blockId = filterUi.blockId;
      const block = blockId !== null ? blockStoreRef.current.blockById(blockId) : null;
      const access = blockRowAccess();
      if (!block || block.outputStartRow === undefined || !access) return;
      const delta = reanchorDelta(block, access);
      if (delta === null) return;
      scrollToBufferRow(block.outputStartRow + delta + lineOffset);
    };

    const previewSeed = seedPreview
      ? gardenSeeds.find((seed) => seed.id === seedPreview.seedId) ?? null
      : null;

    return (
      <div className="ghostty-terminal-frame">
      <div
        ref={containerRef}
        className={`terminal-container ghostty-terminal${linkCursorActive ? ' ghostty-terminal-link-hover' : ''}${annotationCursorActive ? ' ghostty-terminal-annotation-hover' : ''}`}
        data-terminal-renderer="ghostty-webgl"
        tabIndex={0}
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-label="Terminal input"
        aria-multiline="true"
        spellCheck={false}
        onBeforeInput={(event) => event.preventDefault()}
        onPasteCapture={(event) => {
          const hasImage = Array.from(event.clipboardData.items).some((item) => (
            item.kind === 'file' && item.type.startsWith('image/')
          ));
          if (!hasImage) return;
          // Paste events cannot send image bytes through a PTY; both TUIs read the native clipboard on Ctrl+V.
          event.preventDefault();
          event.stopPropagation();
          onInputRef.current('\x16');
        }}
        onWheel={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          if (event.defaultPrevented) return;
          closeSeedPreview();
          const terminal = terminalRef.current;
          const renderer = rendererRef.current;
          if (!terminal || !renderer) return;
          event.preventDefault();
          const wheel = consumeWheelRows(
            event.deltaY,
            event.deltaMode,
            renderer.cellHeight,
            terminal.rows,
            wheelRemainderRowsRef.current,
          );
          wheelRemainderRowsRef.current = wheel.remainderRows;
          if (wheel.lines === 0) return;
          const mouseTracking = terminal.hasMouseTracking();
          if (mouseTracking || terminal.isAlternateScreen()) {
            if (selectionRef.current && !applicationSelectionAnchorRef.current) {
              const range = normalizeSelection(selectionRef.current);
              const text = textForSelectionRange(range);
              if (text) {
                selectedTextRef.current = text;
                applicationSelectionAnchorRef.current = createApplicationSelectionAnchor(
                  range,
                  (row) => selectionLineAtBufferRow(row, 0, terminal.cols).trimEnd(),
                );
              }
            }
            const cell = cellFromPointer(event);
            // Without mouse tracking the wheel becomes arrow keys, which the
            // composer reads as typing; only a real mouse report is pointer input.
            onInputRef.current(applicationWheelInput(
              wheel.lines,
              (cell?.col ?? 0) + 1,
              (cell?.row ?? 0) + 1,
              mouseTracking,
              terminal.getMode(1006),
            ), mouseTracking ? 'pointer' : undefined);
            return;
          }
          const scrollbackLength = terminal.getScrollbackLength();
          const nextOffset = Math.max(0, Math.min(
            scrollbackLength,
            viewportOffsetRef.current - wheel.lines,
          ));
          if (nextOffset !== viewportOffsetRef.current) {
            hoverGenerationRef.current += 1;
          }
          viewportOffsetRef.current = nextOffset;
          if ((nextOffset === 0 && wheel.lines > 0) || (nextOffset === scrollbackLength && wheel.lines < 0)) {
            wheelRemainderRowsRef.current = 0;
          }
          scheduleScrollRender();
        }}
        onMouseDown={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          closeSeedPreview();
          containerRef.current?.focus();
          const terminal = terminalRef.current;
          const cell = cellFromPointer(event);
          if (!terminal || !cell) return;
          if (event.button === 2) {
            sendTrackedMouse('press', event);
            return;
          }
          const link = linkAtCell(cell);
          const opensUri = Boolean(link && (event.metaKey || event.ctrlKey));
          recordPointerHitTest('mousedown', event, {
            activeCell: cell,
            activeUri: link ? link.uri ?? link.absolutePath ?? null : null,
            opensUri,
            phase: 'before-selection',
          });
          if (!opensUri && !event.altKey && sendTrackedMouse('press', event)) return;
          const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          if (event.detail === 3) {
            selectionRef.current = { startRow: row, startCol: 0, endRow: row, endCol: terminal.cols };
            applicationSelectionAnchorRef.current = null;
            selectedBlockIdRef.current = null;
            selectingRef.current = false;
            const rowText = textForSelectionRange(selectionRef.current);
            selectedTextRef.current = rowText || null;
            renderSurface(true);
            if (rowText) void writeClipboardText(rowText);
            return;
          }
          selectedTextRef.current = null;
          applicationSelectionAnchorRef.current = null;
          selectedBlockIdRef.current = null;
          annotationDragRef.current = event.altKey && Boolean(annotationsRef.current);
          annotationClickRef.current = event.altKey ? annotationAtCell(cell) : null;
          selectingRef.current = true;
          selectionPointerStartRef.current = { clientX: event.clientX, clientY: event.clientY };
          selectionDragThresholdMetRef.current = false;
          selectionRef.current = { startRow: row, startCol: cell.col, endRow: row, endCol: cell.col };
          renderSurface(true);
          startSelectionDrag();
        }}
        onMouseMove={(event) => {
          // Sampled, not per pixel: the daemon's quiet window is five seconds.
          const pointerActivityAt = performance.now();
          if (pointerActivityAt - lastPointerActivityAtRef.current >= 250) {
            lastPointerActivityAtRef.current = pointerActivityAt;
            onPointerActivityRef.current?.();
          }
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          if (selectingRef.current) return;
          const hoveredCell = cellFromPointer(event);
          hoveredCellRef.current = hoveredCell;
          acceleratorHeldRef.current = event.metaKey || event.ctrlKey;
          altHeldRef.current = event.altKey;
          syncAnnotationHover(hoveredCell, event.altKey);
          detectHoverLink(hoveredCell);
          syncSeedPreview(hoveredCell);
          updateLinkCursor(hoveredCell, acceleratorHeldRef.current);
          const hoveredLink = hoverLinkAtCell(hoveredCell);
          const hoveredUri = hoveredLink ? hoveredLink.uri ?? hoveredLink.absolutePath ?? null : null;
          if (acceleratorHeldRef.current || hoveredUri) {
            recordPointerHitTest('mousemove', event, {
              activeCell: hoveredCell,
              activeUri: hoveredUri,
              phase: 'hover',
            });
          }
          sendTrackedMouse('move', event);
        }}
        onMouseLeave={() => {
          hoveredCellRef.current = null;
          detectHoverLink(null);
          scheduleSeedPreviewClose();
          setLinkCursorActive(false);
          syncAnnotationHover(null, false);
        }}
        onContextMenu={(event) => {
          event.preventDefault();
          const terminal = terminalRef.current;
          if (!terminal) return;
          if (terminal.hasMouseTracking()) return;
          const cell = cellFromPointer(event);
          let blockId: number | null = null;
          const access = blockRowAccess();
          if (cell && access && blockStoreRef.current.hasBlocks()) {
            const bufferRow = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
            blockId = blockStoreRef.current.blockAtAnchored(bufferRow, access)?.id ?? null;
          }
          if (blockId !== null && selectedBlockIdRef.current !== blockId) {
            selectBlock(blockId);
          }
          const frameRect = containerRef.current?.parentElement?.getBoundingClientRect();
          setContextMenu({
            x: event.clientX - (frameRect?.left ?? 0),
            y: event.clientY - (frameRect?.top ?? 0),
            blockId,
          });
        }}
        onMouseUp={(event) => {
          if (isWorkspaceResizeActive(containerRef.current)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          if (selectingRef.current) return;
          recordPointerHitTest('mouseup', event, {
            phase: 'tracked-mouse-release',
          });
          sendTrackedMouse('release', event);
        }}
        onDoubleClick={async (event) => {
          const terminal = terminalRef.current;
          const cell = cellFromPointer(event);
          recordPointerHitTest('doubleclick', event, {
            activeCell: cell,
            phase: 'before-word-selection',
          });
          if (!terminal || !cell || (terminal.hasMouseTracking() && !event.altKey)) return;
          const range = wordRangeAtColumn(lineAtVisibleRow(cell.row), cell.col);
          if (!range) return;
          const row = bufferRowFromViewportRow(cell.row, terminal.getScrollbackLength(), viewportOffsetRef.current);
          selectionRef.current = {
            startRow: row,
            startCol: range.startCol,
            endRow: row,
            endCol: range.endCol,
          };
          applicationSelectionAnchorRef.current = null;
          selectedBlockIdRef.current = null;
          const text = textForSelectionRange(selectionRef.current);
          selectedTextRef.current = text || null;
          renderSurface(true);
          if (text) await writeClipboardText(text);
        }}
        onCopy={(event) => {
          // In the packaged app the native Edit > Copy menu intercepts Cmd+C, and WebKit fires
          // this clipboard event instead of keydown.
          const text = selectedTextRef.current ?? selectedBlockCopyText(true);
          if (!text) return;
          event.preventDefault();
          if (event.clipboardData) {
            event.clipboardData.setData('text/plain', text);
          } else {
            void writeClipboardText(text);
          }
        }}
        onKeyDown={(event) => {
          const chord = terminalClipboardChord(event);
          if (chord === null) return;
          if (chord === 'paste') {
            if (!isMacLikePlatform()) {
              void pasteFromClipboard();
              event.preventDefault();
            }
            return;
          }
          if (chord === 'copyCommand') {
            const text = selectedMarkdown();
            if (text) {
              void writeClipboardText(text);
              event.preventDefault();
            } else if (copySelectedBlock(false)) {
              event.preventDefault();
            }
            return;
          }
          if (selectedTextRef.current) {
            void writeClipboardText(selectedTextRef.current);
            event.preventDefault();
          } else if (copySelectedBlock(true)) {
            event.preventDefault();
          }
        }}
      >
        {/* Keyed on rendererEpoch: a lost context needs a fresh DOM node. */}
        <canvas ref={canvasRef} key={rendererEpoch} />
        {error && <div className="ghostty-terminal-error">{error}</div>}
      </div>
      {seedMarkLayout ? (
        <div className="ghostty-terminal-seed-marks" aria-hidden="true">
          {seedMarkLayout.segments.map((segment, index) => (
            <span
              key={`${segment.seedId}:${segment.row}:${segment.startCol}:${index}`}
              className={`ghostty-terminal-seed-mark${seedPreview?.seedId === segment.seedId ? ' is-previewing' : ''}`}
              data-terminal-seed-id={segment.seedId}
              style={{
                left: seedMarkLayout.canvasLeft + segment.startCol * seedMarkLayout.cellWidth,
                top: seedMarkLayout.canvasTop + segment.row * seedMarkLayout.cellHeight,
                width: (segment.endCol - segment.startCol) * seedMarkLayout.cellWidth,
                height: seedMarkLayout.cellHeight,
              }}
            />
          ))}
        </div>
      ) : null}
      {previewSeed && seedPreview ? (
        <TerminalSeedPreview
          seed={previewSeed}
          anchor={seedPreview.anchor}
          onOpen={(seedId) => {
            onOpenSeedRef.current?.(seedId);
            closeSeedPreview();
          }}
          onClose={closeSeedPreview}
          onPointerEnter={() => {
            seedPreviewPointerInsideRef.current = true;
            cancelSeedPreviewClose();
          }}
          onPointerLeave={() => {
            seedPreviewPointerInsideRef.current = false;
            scheduleSeedPreviewClose();
          }}
        />
      ) : null}
      {findUi.open && (
        <div className="ghostty-find-bar" data-testid="ghostty-find-bar">
          <input
            ref={findInputRef}
            className="ghostty-find-input"
            data-testid="ghostty-find-input"
            type="text"
            placeholder="Find"
            spellCheck={false}
            autoComplete="off"
            defaultValue={findQueryRef.current}
            onChange={(event) => {
              findQueryRef.current = event.target.value;
              if (findRescanTimerRef.current) clearTimeout(findRescanTimerRef.current);
              findRescanTimerRef.current = setTimeout(() => {
                findRescanTimerRef.current = null;
                runFindScan();
              }, 150);
            }}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.preventDefault();
                closeFind();
              } else if (event.key === 'Enter') {
                event.preventDefault();
                findNavigate(event.shiftKey ? 1 : -1);
              }
              event.stopPropagation();
            }}
          />
          <button
            type="button"
            className={`ghostty-find-button ghostty-find-case${findUi.caseSensitive ? ' ghostty-find-case-active' : ''}`}
            aria-label="Match case"
            aria-pressed={findUi.caseSensitive}
            title="Match case"
            onClick={() => {
              findCaseSensitiveRef.current = !findCaseSensitiveRef.current;
              setFindUi((ui) => ({ ...ui, caseSensitive: findCaseSensitiveRef.current }));
              runFindScan();
              findInputRef.current?.focus();
            }}
          >
            Aa
          </button>
          <span className="ghostty-find-count" data-testid="ghostty-find-count">
            {findUi.matchCount > 0 ? `${findUi.focusedIndex + 1}/${findUi.matchCount}` : '0/0'}
          </span>
          <button
            type="button"
            className="ghostty-find-button"
            aria-label="Previous match"
            title="Previous match (Enter)"
            onClick={() => { findNavigate(-1); findInputRef.current?.focus(); }}
          >
            ▲
          </button>
          <button
            type="button"
            className="ghostty-find-button"
            aria-label="Next match"
            title="Next match (Shift+Enter)"
            onClick={() => { findNavigate(1); findInputRef.current?.focus(); }}
          >
            ▼
          </button>
          <button
            type="button"
            className="ghostty-find-button"
            aria-label="Close find"
            title="Close (Esc)"
            onClick={closeFind}
          >
            ✕
          </button>
        </div>
      )}
      {filterUi.open && (
        <div className="ghostty-filter-panel" data-testid="ghostty-filter-panel">
          <div className="ghostty-filter-bar">
            <input
              ref={filterInputRef}
              className="ghostty-find-input ghostty-filter-input"
              data-testid="ghostty-filter-input"
              type="text"
              placeholder="Filter block output"
              spellCheck={false}
              autoComplete="off"
              defaultValue=""
              onChange={(event) => {
                filterQueryRef.current = event.target.value;
                if (filterRescanTimerRef.current) clearTimeout(filterRescanTimerRef.current);
                filterRescanTimerRef.current = setTimeout(() => {
                  filterRescanTimerRef.current = null;
                  runBlockFilter(filterUi.blockId, filterUi.caseSensitive);
                }, 150);
              }}
              onKeyDown={(event) => {
                if (event.key === 'Escape') {
                  event.preventDefault();
                  closeBlockFilter();
                }
                event.stopPropagation();
              }}
            />
            <button
              type="button"
              className={`ghostty-find-button ghostty-find-case${filterUi.caseSensitive ? ' ghostty-find-case-active' : ''}`}
              aria-label="Match case"
              aria-pressed={filterUi.caseSensitive}
              title="Match case"
              onClick={() => {
                const caseSensitive = !filterUi.caseSensitive;
                setFilterUi((ui) => ({ ...ui, caseSensitive }));
                runBlockFilter(filterUi.blockId, caseSensitive);
                filterInputRef.current?.focus();
              }}
            >
              Aa
            </button>
            <span className="ghostty-find-count" data-testid="ghostty-filter-count">
              {filterMatches.length} {filterMatches.length === 1 ? 'line' : 'lines'}
            </span>
            <button
              type="button"
              className="ghostty-find-button"
              aria-label="Close filter"
              title="Close (Esc)"
              onClick={closeBlockFilter}
            >
              ✕
            </button>
          </div>
          {filterQueryRef.current && (
            <div className="ghostty-filter-results" data-testid="ghostty-filter-results">
              {filterMatches.length === 0 ? (
                <div className="ghostty-filter-empty">No matching lines</div>
              ) : (
                filterMatches.map((line) => (
                  <button
                    key={line.lineOffset}
                    type="button"
                    className="ghostty-filter-line"
                    title="Scroll to line"
                    onClick={() => scrollToFilteredLine(line.lineOffset)}
                  >
                    {lineSegments(line).map((segment, index) => (
                      segment.match
                        ? <mark key={index} className="ghostty-filter-match">{segment.text}</mark>
                        : <span key={index}>{segment.text}</span>
                    ))}
                  </button>
                ))
              )}
            </div>
          )}
        </div>
      )}
      {contextMenu && (
        <TerminalContextMenu
          position={{ x: contextMenu.x, y: contextMenu.y }}
          items={contextMenuItems}
          onSelect={handleContextMenuSelect}
          onClose={() => setContextMenu(null)}
        />
      )}
      </div>
    );
  },
);
