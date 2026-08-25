import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  CodeView,
  useStableCallback,
  type AnnotationSide,
  type CodeViewHandle,
  type CodeViewItem,
  type DiffLineAnnotation,
  type FileContents,
  type SelectedLineRange,
} from '@pierre/diffs/react';
// These two are exported from the package root, not the /react entry.
import { parseDiffFromFile, type CodeViewLineSelection, type CodeViewOptions } from '@pierre/diffs';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import type { ResolvedTheme } from '../../hooks/useTheme';
import type { ReviewComment } from '../../types/generated';
import { isOriginalSideComment } from '../../utils/reviewComment';
import { normalizeRange } from '../DiffView';
import { DiffCommentThread } from '../DiffCommentThread';
import { Markdown } from '../Markdown';
import '../DiffView.css';
import './PresentTour.css';

export interface PresentTourFileDiff {
  loading: boolean;
  original?: string;
  modified?: string;
  error?: string;
}

export interface PresentTourFile {
  path: string;
  note?: string;
  diff: PresentTourFileDiff;
  group?: 'tour' | 'other' | 'skip';
}

export interface AnnotationAnchor {
  path: string;
  anchorKey: string;
}

export interface PresentTourProps {
  summary?: string;
  summaryVisible?: boolean;
  onSummaryVisibleChange?: (visible: boolean) => void;
  files: PresentTourFile[];
  comments: ReviewComment[];
  editingCommentId: string | null;
  readOnlyCommentIds: Set<string>;
  resolvedTheme?: ResolvedTheme;
  fontSize?: number;
  onAddComment: (filepath: string, lineStart: number, lineEnd: number, content: string) => void;
  onEditComment: (id: string, content: string) => void;
  onStartEdit: (id: string) => void;
  onCancelEdit: () => void;
  onResolveComment: (id: string, resolved: boolean) => void;
  onDeleteComment: (id: string) => void;
  onSendToClaude?: (reference: string) => void;
  scrollToPath?: string | null;
  scrollNonce?: number;
  onActivePathChange?: (path: string | null) => void;
  reviewedPaths: ReadonlySet<string>;
  onToggleReviewed: (path: string) => void;
  annotationCommentIds?: Set<string>;
  onAnnotationAnchorsChange?: (anchors: AnnotationAnchor[]) => void;
  scrollToAnnotation?: AnnotationAnchor | null;
  annotationScrollNonce?: number;
}

interface AnnotationMeta {
  filepath: string;
  side: AnnotationSide;
  lineNumber: number;
  comments: ReviewComment[];
  draft: boolean;
  anchorKey: string;
  outsideDiffNote?: string;
  kind?: 'note';
  noteMarkdown?: string;
}

type DraftAnchor = {
  filepath: string;
  side: AnnotationSide;
  start: number;
  end: number;
};

function anchorKeyOf(filepath: string, side: AnnotationSide, start: number): string {
  return `${filepath}:${side}:${start}`;
}

type VisibleLineRanges = Record<AnnotationSide, Array<[number, number]>>;

function isLineInRanges(line: number, ranges: Array<[number, number]>): boolean {
  return ranges.some(([start, end]) => line >= start && line <= end);
}

function nearestVisibleLine(target: number, ranges: Array<[number, number]>): number | null {
  let best: number | null = null;
  let bestDistance = Infinity;
  for (const [start, end] of ranges) {
    const clamped = Math.max(start, Math.min(end, target));
    const distance = Math.abs(target - clamped);
    if (distance < bestDistance || (distance === bestDistance && best !== null && clamped < best)) {
      bestDistance = distance;
      best = clamped;
    }
  }
  return best;
}

function fileHasMermaid(file: PresentTourFile, fileComments: ReviewComment[]): boolean {
  if (file.note?.includes('```mermaid')) return true;
  return fileComments.some((c) => c.content.includes('```mermaid'));
}

interface ParsedFileCacheEntry {
  original: string;
  modified: string;
  fileDiff: ReturnType<typeof parseDiffFromFile>;
  visibleLineRanges: VisibleLineRanges;
  lineCounts: { additions: number; deletions: number };
}

interface FileItemCacheEntry {
  signature: string;
  shownOriginal?: string;
  shownModified?: string;
  item: CodeViewItem<AnnotationMeta>;
  anchors: AnnotationAnchor[];
  notePlaced: boolean;
}

function getVisibleLineRangesFromDiff(diff: ReturnType<typeof parseDiffFromFile>): VisibleLineRanges {
  return diff.hunks.reduce<VisibleLineRanges>(
    (ranges, hunk) => {
      ranges.deletions.push([hunk.deletionStart, hunk.deletionStart + hunk.deletionCount - 1]);
      ranges.additions.push([hunk.additionStart, hunk.additionStart + hunk.additionCount - 1]);
      return ranges;
    },
    { additions: [], deletions: [] }
  );
}

function ensureParsedFile(
  cache: Map<string, ParsedFileCacheEntry>,
  path: string,
  original: string,
  modified: string
): ParsedFileCacheEntry {
  const cached = cache.get(path);
  if (cached && cached.original === original && cached.modified === modified) return cached;
  const oldFile: FileContents = { name: path, contents: original };
  const newFile: FileContents = { name: path, contents: modified };
  const fileDiff = parseDiffFromFile(oldFile, newFile);
  const visibleLineRanges = getVisibleLineRangesFromDiff(fileDiff);
  const lineCounts = {
    additions: modified.split('\n').length,
    deletions: original.split('\n').length,
  };
  const entry: ParsedFileCacheEntry = { original, modified, fileDiff, visibleLineRanges, lineCounts };
  cache.set(path, entry);
  return entry;
}

export function PresentTour({
  summary,
  summaryVisible = true,
  onSummaryVisibleChange,
  files,
  comments,
  editingCommentId,
  readOnlyCommentIds,
  resolvedTheme = 'dark',
  fontSize,
  onAddComment,
  onEditComment,
  onStartEdit,
  onCancelEdit,
  onResolveComment,
  onDeleteComment,
  onSendToClaude,
  scrollToPath,
  scrollNonce,
  onActivePathChange,
  reviewedPaths,
  onToggleReviewed,
  annotationCommentIds,
  onAnnotationAnchorsChange,
  scrollToAnnotation,
  annotationScrollNonce,
}: PresentTourProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<CodeViewHandle<AnnotationMeta> | null>(null);
  const suppressSelectionEndRef = useRef(false);
  const summaryBodyRef = useRef<HTMLDivElement>(null);
  const handleSummaryWheel = useCallback(
    (e: React.WheelEvent) => {
      if (!summaryVisible || e.deltaY <= 0) return;
      const body = summaryBodyRef.current;
      if (body && body.scrollTop + body.clientHeight < body.scrollHeight - 1) return;
      onSummaryVisibleChange?.(false);
    },
    [summaryVisible, onSummaryVisibleChange]
  );
  const annotationAnchorsRef = useRef<AnnotationAnchor[]>([]);
  // CodeView computes header heights from a global constant, so a taller note through
  // `renderHeaderMetadata` breaks the layout math; annotation slots are DOM-measured.
  const notePlacedPathsRef = useRef<Set<string>>(new Set());
  // CodeView's `syncItemRecord` keeps the cached record on a matching `version`,
  // two `undefined`s included, so annotations without a bump never reach the DOM.
  const itemsVersionRef = useRef(0);
  const parseCacheRef = useRef<Map<string, ParsedFileCacheEntry>>(new Map());
  const fileItemCacheRef = useRef<Map<string, FileItemCacheEntry>>(new Map());
  const pendingPathsRef = useRef<Set<string>>(new Set());
  const [readyPaths, setReadyPaths] = useState<ReadonlySet<string>>(() => new Set());

  // CodeView's cached layout never learns that a settling mermaid diagram grew,
  // so completion forces a rAF-coalesced `version` bump.
  const [diagramLayoutTick, setDiagramLayoutTick] = useState(0);
  const diagramLayoutRafRef = useRef<number | null>(null);
  const handleDiagramLayoutChange = useCallback(() => {
    if (diagramLayoutRafRef.current !== null) return;
    diagramLayoutRafRef.current = requestAnimationFrame(() => {
      diagramLayoutRafRef.current = null;
      setDiagramLayoutTick((tick) => tick + 1);
    });
  }, []);
  useEffect(() => {
    return () => {
      if (diagramLayoutRafRef.current !== null) cancelAnimationFrame(diagramLayoutRafRef.current);
    };
  }, []);

  const [drafts, setDrafts] = useState<Record<string, DraftAnchor>>({});
  const draftKeys = useMemo(() => Object.keys(drafts), [drafts]);

  // Outside React state: state a keystroke touches bumps the file's version on
  // every character typed.
  const draftContentsRef = useRef<Map<string, string>>(new Map());

  const openDraft = useCallback((filepath: string, side: AnnotationSide, start: number, end: number) => {
    const key = anchorKeyOf(filepath, side, start);
    setDrafts((current) => {
      if (current[key]) return current;
      draftContentsRef.current.set(key, '');
      return { ...current, [key]: { filepath, side, start, end } };
    });
  }, []);

  const updateDraftContent = useCallback((key: string, content: string) => {
    draftContentsRef.current.set(key, content);
  }, []);

  const closeDraft = useCallback((key: string) => {
    draftContentsRef.current.delete(key);
    setDrafts((current) => {
      if (!(key in current)) return current;
      const { [key]: _removed, ...rest } = current;
      return rest;
    });
  }, []);

  const handleEscapeDraft = useCallback(() => {
    if (draftKeys.length === 0) return;
    closeDraft(draftKeys[draftKeys.length - 1]);
  }, [draftKeys, closeDraft]);
  useEscapeStack(handleEscapeDraft, draftKeys.length > 0);
  useEscapeStack(onCancelEdit, editingCommentId !== null);

  const formOpenByFile = useMemo(() => {
    const open = new Set<string>();
    for (const key of draftKeys) open.add(drafts[key].filepath);
    if (editingCommentId) {
      const editing = comments.find((c) => c.id === editingCommentId);
      if (editing) open.add(editing.filepath);
    }
    return open;
  }, [draftKeys, drafts, editingCommentId, comments]);

  const frozenRef = useRef<Map<string, { original: string; modified: string }>>(new Map());
  for (const path of Array.from(frozenRef.current.keys())) {
    if (!formOpenByFile.has(path)) frozenRef.current.delete(path);
  }

  const commentsByFile = useMemo(() => {
    const map = new Map<string, ReviewComment[]>();
    for (const c of comments) {
      const list = map.get(c.filepath);
      if (list) list.push(c);
      else map.set(c.filepath, [c]);
    }
    return map;
  }, [comments]);

  const draftsByFile = useMemo(() => {
    const map = new Map<string, string[]>();
    for (const key of draftKeys) {
      const filepath = drafts[key].filepath;
      const list = map.get(filepath);
      if (list) list.push(key);
      else map.set(filepath, [key]);
    }
    return map;
  }, [draftKeys, drafts]);

  const tourMounted = files.length > 0;

  const allSettled =
    files.length > 0 &&
    files.every((f) => {
      if (f.diff.loading) return false;
      const isErrorCard = Boolean(f.diff.error) || f.diff.original === undefined || f.diff.modified === undefined;
      return isErrorCard || readyPaths.has(f.path);
    });

  useEffect(() => {
    const admittable = files.filter(
      (f) => !f.diff.loading && !f.diff.error && f.diff.original !== undefined && f.diff.modified !== undefined && !readyPaths.has(f.path)
    );
    const currentPaths = new Set(files.map((f) => f.path));
    const stale = Array.from(readyPaths).filter((p) => !currentPaths.has(p));
    if (admittable.length === 0 && stale.length === 0) return;

    const raf = requestAnimationFrame(() => {
      const sliceStart = performance.now();
      const admitted: string[] = [];
      const SLICE_BUDGET_MS = 8;
      for (const file of admittable) {
        ensureParsedFile(parseCacheRef.current, file.path, file.diff.original as string, file.diff.modified as string);
        admitted.push(file.path);
        if (performance.now() - sliceStart > SLICE_BUDGET_MS) break;
      }
      setReadyPaths((current) => {
        const next = new Set(current);
        for (const path of admitted) next.add(path);
        for (const path of stale) next.delete(path);
        return next;
      });
    });
    return () => cancelAnimationFrame(raf);
  }, [files, readyPaths]);

  const handleSaveDraft = useCallback(
    async (key: string, content: string) => {
      const d = drafts[key];
      if (!d) return;
      const lineStart = d.start;
      const lineEnd = d.side === 'deletions' ? -d.end : d.end;
      try {
        onAddComment(d.filepath, lineStart, lineEnd, content);
        closeDraft(key);
      } catch {
      }
    },
    [drafts, onAddComment, closeDraft]
  );

  const handleSendComment = useCallback(
    (comment: ReviewComment) => {
      if (!onSendToClaude) return;
      const ref = normalizeRange({ side: isOriginalSideComment(comment) ? 'deletions' : 'additions', start: comment.line_start, end: Math.abs(comment.line_end) });
      if (!ref) return;
      onSendToClaude(`@${comment.filepath}:L${ref.start}${ref.start === ref.end ? '' : `-L${ref.end}`}\nComment: ${comment.content}`);
    },
    [onSendToClaude]
  );

  const renderAnnotation = useCallback(
    (annotation: DiffLineAnnotation<AnnotationMeta>) => {
      const meta = annotation.metadata;
      if (!meta) return null;
      const key = meta.anchorKey;
      if (meta.kind === 'note') {
        return (
          <div key={key} className="present-tour-file-note-slot">
            <Markdown className="present-tour-file-note" onDiagramLayoutChange={handleDiagramLayoutChange}>
              {meta.noteMarkdown ?? ''}
            </Markdown>
          </div>
        );
      }
      return (
        // CodeView exposes no id hook on annotation slots; the N/P scroll effect
        // finds the thread by this attribute.
        <div key={key} data-anchor-key={key}>
          <DiffCommentThread
            comments={meta.comments}
            draft={meta.draft}
            editingCommentId={editingCommentId}
            readOnlyCommentIds={readOnlyCommentIds}
            showSendToClaude={!!onSendToClaude}
            draftContent={meta.draft ? draftContentsRef.current.get(key) ?? '' : undefined}
            onDraftContentChange={meta.draft ? (content) => updateDraftContent(key, content) : undefined}
            onSaveDraft={(content) => handleSaveDraft(key, content)}
            onCancelDraft={() => closeDraft(key)}
            onStartEdit={onStartEdit}
            onEditComment={onEditComment}
            onCancelEdit={onCancelEdit}
            onResolveComment={onResolveComment}
            onDeleteComment={onDeleteComment}
            onSendComment={handleSendComment}
            caption={meta.outsideDiffNote}
            onReply={meta.draft ? undefined : () => openDraft(meta.filepath, meta.side, meta.lineNumber, meta.lineNumber)}
            onDiagramLayoutChange={handleDiagramLayoutChange}
          />
        </div>
      );
    },
    [
      editingCommentId,
      readOnlyCommentIds,
      onSendToClaude,
      updateDraftContent,
      handleSaveDraft,
      closeDraft,
      onStartEdit,
      onEditComment,
      onCancelEdit,
      onResolveComment,
      onDeleteComment,
      handleSendComment,
      openDraft,
      handleDiagramLayoutChange,
    ]
  );

  const items = useMemo<CodeViewItem<AnnotationMeta>[]>(() => {
    annotationAnchorsRef.current = [];
    notePlacedPathsRef.current = new Set();
    pendingPathsRef.current = new Set();

    const result = files.map((file): CodeViewItem<AnnotationMeta> => {
      const { diff } = file;
      const cached = fileItemCacheRef.current.get(file.path);

      if (diff.error || (!diff.loading && (diff.original === undefined || diff.modified === undefined))) {
        const signature = JSON.stringify(['error', diff.error ?? 'Failed to load this file’s diff.']);
        if (cached && cached.signature === signature && cached.shownOriginal === undefined && cached.shownModified === undefined) {
          if (cached.notePlaced) notePlacedPathsRef.current.add(file.path);
          annotationAnchorsRef.current.push(...cached.anchors);
          return cached.item;
        }
        const item: CodeViewItem<AnnotationMeta> = {
          id: file.path,
          type: 'file',
          file: { name: file.path, contents: diff.error ?? 'Failed to load this file’s diff.' },
          version: ++itemsVersionRef.current,
        };
        fileItemCacheRef.current.set(file.path, { signature, item, anchors: [], notePlaced: false });
        return item;
      }

      if (diff.loading || !readyPaths.has(file.path)) {
        pendingPathsRef.current.add(file.path);
        const signature = JSON.stringify(['pending']);
        if (cached && cached.signature === signature) return cached.item;
        const emptyFile: FileContents = { name: file.path, contents: '' };
        const item: CodeViewItem<AnnotationMeta> = {
          id: file.path,
          type: 'diff',
          fileDiff: parseDiffFromFile(emptyFile, emptyFile),
          annotations: [],
          version: ++itemsVersionRef.current,
        };
        fileItemCacheRef.current.set(file.path, {
          signature,
          shownOriginal: undefined,
          shownModified: undefined,
          item,
          anchors: [],
          notePlaced: false,
        });
        return item;
      }

      const original = diff.original as string;
      const modified = diff.modified as string;

      const frozen = frozenRef.current.get(file.path);
      if (formOpenByFile.has(file.path) && !frozen) {
        frozenRef.current.set(file.path, { original, modified });
      }
      const shown = frozenRef.current.get(file.path) ?? { original, modified };

      const fileComments = commentsByFile.get(file.path) ?? [];
      const fileDraftKeys = draftsByFile.get(file.path) ?? [];
      const editingBelongsToFile =
        fileComments.some((c) => c.id === editingCommentId) || fileDraftKeys.includes(editingCommentId ?? '');
      const hasMermaid = fileHasMermaid(file, fileComments);

      const signature = JSON.stringify([
        'diff',
        file.note ?? null,
        fileComments.map((c) => [
          c.id,
          c.content,
          c.line_start,
          c.line_end,
          c.resolved,
          c.resolved_by ?? null,
          c.author,
          annotationCommentIds?.has(c.id) ?? false,
        ]),
        fileDraftKeys.map((key) => {
          const d = drafts[key];
          return [key, d.side, d.start, d.end];
        }),
        editingBelongsToFile ? editingCommentId : null,
        reviewedPaths.has(file.path),
        hasMermaid ? diagramLayoutTick : null,
      ]);

      if (cached && cached.signature === signature && cached.shownOriginal === shown.original && cached.shownModified === shown.modified) {
        if (cached.notePlaced) notePlacedPathsRef.current.add(file.path);
        annotationAnchorsRef.current.push(...cached.anchors);
        return cached.item;
      }

      const { fileDiff, visibleLineRanges, lineCounts } = ensureParsedFile(parseCacheRef.current, file.path, shown.original, shown.modified);

      const groups = new Map<string, AnnotationMeta>();
      for (const comment of fileComments) {
        const side: AnnotationSide = isOriginalSideComment(comment) ? 'deletions' : 'additions';
        const max = side === 'deletions' ? lineCounts.deletions : lineCounts.additions;
        const lineExists = comment.line_start >= 1 && comment.line_start <= max;
        if (!lineExists) continue;
        const ranges = visibleLineRanges[side];
        let line = comment.line_start;
        let outsideDiffNote: string | undefined;
        if (!isLineInRanges(line, ranges)) {
          if (!annotationCommentIds?.has(comment.id)) continue;
          const nearest = nearestVisibleLine(line, ranges);
          if (nearest === null) continue;
          line = nearest;
          const originalEnd = Math.abs(comment.line_end);
          const rangeText = comment.line_start === originalEnd ? `${comment.line_start}` : `${comment.line_start}–${originalEnd}`;
          outsideDiffNote = `refers to line ${rangeText}, outside the visible diff`;
        }
        const key = anchorKeyOf(file.path, side, line);
        let group = groups.get(key);
        if (!group) {
          group = { filepath: file.path, side, lineNumber: line, comments: [], draft: false, anchorKey: key };
          groups.set(key, group);
        }
        if (outsideDiffNote && !group.outsideDiffNote) group.outsideDiffNote = outsideDiffNote;
        group.comments.push(comment);
      }
      for (const key of fileDraftKeys) {
        const d = drafts[key];
        let group = groups.get(key);
        if (!group) {
          group = { filepath: file.path, side: d.side, lineNumber: d.start, comments: [], draft: true, anchorKey: key };
          groups.set(key, group);
        } else {
          group.draft = true;
        }
      }

      const all = Array.from(groups.values());

      const fileAnnotationGroups = all
        .filter((g) => g.comments.some((c) => annotationCommentIds?.has(c.id)))
        .sort((a, b) => a.lineNumber - b.lineNumber);
      const fileAnchors: AnnotationAnchor[] = fileAnnotationGroups.map((g) => ({ path: file.path, anchorKey: g.anchorKey }));
      annotationAnchorsRef.current.push(...fileAnchors);

      const hasOpenForm = (g: AnnotationMeta) => g.draft || g.comments.some((c) => c.id === editingCommentId);
      const active = all.filter(hasOpenForm).sort((a, b) => a.anchorKey.localeCompare(b.anchorKey));
      const rest = all.filter((g) => !hasOpenForm(g));

      let noteAnnotation: DiffLineAnnotation<AnnotationMeta> | undefined;
      let notePlaced = false;
      if (file.note) {
        const additionsStart = visibleLineRanges.additions[0]?.[0];
        const deletionsStart = visibleLineRanges.deletions[0]?.[0];
        const side: AnnotationSide | undefined = additionsStart !== undefined ? 'additions' : deletionsStart !== undefined ? 'deletions' : undefined;
        const lineNumber = side === 'additions' ? additionsStart : deletionsStart;
        if (side !== undefined && lineNumber !== undefined) {
          const noteMeta: AnnotationMeta = {
            kind: 'note',
            noteMarkdown: file.note,
            filepath: file.path,
            side,
            lineNumber,
            comments: [],
            draft: false,
            anchorKey: `note:${file.path}`,
          };
          noteAnnotation = { side, lineNumber, metadata: noteMeta };
          notePlaced = true;
          notePlacedPathsRef.current.add(file.path);
        }
      }

      const annotations: DiffLineAnnotation<AnnotationMeta>[] = [
        ...(noteAnnotation ? [noteAnnotation] : []),
        ...[...active, ...rest].map((meta) => ({
          side: meta.side,
          lineNumber: meta.lineNumber,
          metadata: meta,
        })),
      ];

      const item: CodeViewItem<AnnotationMeta> = { id: file.path, type: 'diff', fileDiff, annotations, version: ++itemsVersionRef.current };
      fileItemCacheRef.current.set(file.path, {
        signature,
        shownOriginal: shown.original,
        shownModified: shown.modified,
        item,
        anchors: fileAnchors,
        notePlaced,
      });
      return item;
    });

    const currentPaths = new Set(files.map((f) => f.path));
    for (const path of Array.from(fileItemCacheRef.current.keys())) {
      if (!currentPaths.has(path)) fileItemCacheRef.current.delete(path);
    }
    for (const path of Array.from(parseCacheRef.current.keys())) {
      if (!currentPaths.has(path)) parseCacheRef.current.delete(path);
    }

    return result;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    files,
    commentsByFile,
    draftsByFile,
    drafts,
    formOpenByFile,
    editingCommentId,
    reviewedPaths,
    annotationCommentIds,
    diagramLayoutTick,
    readyPaths,
  ]);

  useEffect(() => {
    onAnnotationAnchorsChange?.(annotationAnchorsRef.current);
  }, [items, onAnnotationAnchorsChange]);

  const handleGutterUtilityClick = useStableCallback(
    (range: SelectedLineRange, context: { item: CodeViewItem<AnnotationMeta> }) => {
      const normalized = normalizeRange(range);
      if (!normalized) return;
      const { side, start, end } = normalized;
      suppressSelectionEndRef.current = true;
      openDraft(context.item.id, side, start, end);
    }
  );

  const handleLineSelectionEnd = useStableCallback(
    (range: SelectedLineRange | null, context: { item: CodeViewItem<AnnotationMeta> }) => {
      if (suppressSelectionEndRef.current) {
        suppressSelectionEndRef.current = false;
        return;
      }
      if (!range) return;
      const normalized = normalizeRange(range);
      if (!normalized) return;
      const { side, start, end } = normalized;
      openDraft(context.item.id, side, start, end);
    }
  );

  const noteByPath = useMemo(() => new Map(files.map((f) => [f.path, f.note])), [files]);
  const groupByPath = useMemo(() => new Map(files.map((f) => [f.path, f.group ?? 'tour'])), [files]);

  const syncCardClasses = useCallback(() => {
    const instance = handleRef.current?.getInstance();
    if (!instance) return;
    for (const rendered of instance.getRenderedItems()) {
      const isSkip = groupByPath.get(rendered.id) === 'skip';
      rendered.element.classList.toggle('present-tour-card-skip', isSkip);
      rendered.element.classList.toggle('present-tour-card-pending', pendingPathsRef.current.has(rendered.id));
    }
  }, [groupByPath]);

  useEffect(() => {
    if (!tourMounted) return;
    syncCardClasses();
  }, [tourMounted, items, groupByPath, syncCardClasses]);

  const renderHeaderMetadata = useCallback(
    (item: CodeViewItem<AnnotationMeta>) => {
      if (notePlacedPathsRef.current.has(item.id) || pendingPathsRef.current.has(item.id)) return null;
      const note = noteByPath.get(item.id);
      if (!note) return null;
      return (
        <Markdown className="present-tour-file-note" onDiagramLayoutChange={handleDiagramLayoutChange}>
          {note}
        </Markdown>
      );
    },
    [noteByPath, handleDiagramLayoutChange]
  );

  const renderHeaderPrefix = useCallback(
    (item: CodeViewItem<AnnotationMeta>) => {
      if (groupByPath.get(item.id) === 'skip') return null;
      const isReviewed = reviewedPaths.has(item.id);
      return (
        <button
          type="button"
          className={`present-tour-reviewed-toggle ${isReviewed ? 'is-reviewed' : ''}`}
          onClick={(e) => {
            e.stopPropagation();
            onToggleReviewed(item.id);
          }}
          title={isReviewed ? 'Mark as not reviewed' : 'Mark as reviewed'}
        >
          <span className="present-tour-reviewed-check">{isReviewed ? '✓' : '○'}</span>
          <span className="present-tour-reviewed-label">{isReviewed ? 'Reviewed' : 'Mark reviewed'}</span>
          <kbd>R</kbd>
        </button>
      );
    },
    [reviewedPaths, onToggleReviewed, groupByPath]
  );

  const options = useMemo<CodeViewOptions<AnnotationMeta>>(
    () => ({
      diffStyle: 'unified',
      expandUnchanged: false,
      diffIndicators: 'classic',
      theme: { dark: 'pierre-dark', light: 'pierre-light' },
      themeType: resolvedTheme,
      preferredHighlighter: 'shiki-js',
      enableLineSelection: true,
      onLineSelectionEnd: handleLineSelectionEnd,
      enableGutterUtility: true,
      onGutterUtilityClick: handleGutterUtilityClick,
      stickyHeaders: true,
    }),
    [resolvedTheme, handleLineSelectionEnd, handleGutterUtilityClick]
  );

  const selectedLines: CodeViewLineSelection | null = null;

  const userTookOverRef = useRef(false);
  const wasMountedRef = useRef(false);
  const passiveSuppressedRef = useRef(false);
  const suppressQuietTimerRef = useRef<number>(0);
  // `tourMounted` must be a dep: containerRef is null on first render, so an
  // empty dep array attaches these listeners to nothing, forever.
  useEffect(() => {
    const scroller = containerRef.current;
    if (!scroller) return;
    const takeover = () => {
      userTookOverRef.current = true;
      passiveSuppressedRef.current = false;
      window.clearTimeout(suppressQuietTimerRef.current);
    };
    const onNativeScroll = () => {
      if (!userTookOverRef.current && scroller.scrollTop !== 0) scroller.scrollTop = 0;
    };
    scroller.addEventListener('wheel', takeover, { passive: true });
    scroller.addEventListener('touchstart', takeover, { passive: true });
    scroller.addEventListener('pointerdown', takeover, { passive: true });
    scroller.addEventListener('keydown', takeover);
    scroller.addEventListener('scroll', onNativeScroll);
    return () => {
      scroller.removeEventListener('wheel', takeover);
      scroller.removeEventListener('touchstart', takeover);
      scroller.removeEventListener('pointerdown', takeover);
      scroller.removeEventListener('keydown', takeover);
      scroller.removeEventListener('scroll', onNativeScroll);
      window.clearTimeout(suppressQuietTimerRef.current);
    };
  }, [tourMounted]);

  // The library's smooth `scrollTo` fires native `scroll` events the cold-window pin fights
  // back to 0, and it no-ops against an unmeasured layout, so arm the flag and rAF the mount.
  useEffect(() => {
    const wasMounted = wasMountedRef.current;
    wasMountedRef.current = tourMounted;
    const hasRequest = (scrollNonce ?? 0) > 0;
    if (!hasRequest || !tourMounted) return;
    const handle = handleRef.current;
    if (!handle) return;
    userTookOverRef.current = true;
    passiveSuppressedRef.current = true;
    window.clearTimeout(suppressQuietTimerRef.current);
    const performScroll = () => {
      if (scrollToPath) {
        handle.scrollTo({ type: 'item', id: scrollToPath, align: 'start', behavior: 'smooth' });
      } else {
        containerRef.current?.scrollTo({ top: 0, behavior: 'smooth' });
      }
    };
    if (!wasMounted) {
      const raf = requestAnimationFrame(performScroll);
      return () => cancelAnimationFrame(raf);
    }
    performScroll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToPath, scrollNonce, tourMounted, allSettled]);

  // CodeView virtualizes, so the slot may be unmounted for several frames: a fixed attempt
  // count gave up before a cross-file scroll finished, so the retry is time-boxed.
  useEffect(() => {
    if (!scrollToAnnotation) return;
    const hasRequest = (annotationScrollNonce ?? 0) > 0;
    if (!hasRequest || !tourMounted) return;
    const handle = handleRef.current;
    if (!handle) return;
    userTookOverRef.current = true;
    passiveSuppressedRef.current = true;
    window.clearTimeout(suppressQuietTimerRef.current);
    const { path, anchorKey } = scrollToAnnotation;
    handle.scrollTo({ type: 'item', id: path, align: 'start', behavior: 'smooth' });
    const LOCATE_BUDGET_MS = 1500;
    const deadline = Date.now() + LOCATE_BUDGET_MS;
    let raf = 0;
    const tryLocate = () => {
      const el = containerRef.current?.querySelector<HTMLElement>(`[data-anchor-key="${CSS.escape(anchorKey)}"]`);
      if (el) {
        el.scrollIntoView({ block: 'center' });
        return;
      }
      if (Date.now() >= deadline) return;
      raf = requestAnimationFrame(tryLocate);
    };
    raf = requestAnimationFrame(tryLocate);
    return () => cancelAnimationFrame(raf);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToAnnotation, annotationScrollNonce, tourMounted]);

  const handleScroll = useCallback(
    (_scrollTop: number) => {
      syncCardClasses();
      if (!onActivePathChange || !containerRef.current) return;
      if (!userTookOverRef.current) return;
      if (passiveSuppressedRef.current) {
        window.clearTimeout(suppressQuietTimerRef.current);
        suppressQuietTimerRef.current = window.setTimeout(() => {
          passiveSuppressedRef.current = false;
        }, 200);
        return;
      }
      const instance = handleRef.current?.getInstance();
      if (!instance) return;
      const containerTop = containerRef.current.getBoundingClientRect().top;
      const threshold = 80;
      let bestPath: string | null = null;
      let bestTop = -Infinity;
      let nearestPath: string | null = null;
      let nearestDistance = Infinity;
      for (const rendered of instance.getRenderedItems()) {
        const top = rendered.element.getBoundingClientRect().top - containerTop;
        if (top <= threshold && top > bestTop) {
          bestTop = top;
          bestPath = rendered.id;
        }
        const distance = Math.abs(top - threshold);
        if (distance < nearestDistance) {
          nearestDistance = distance;
          nearestPath = rendered.id;
        }
      }
      const path = bestPath ?? nearestPath;
      if (path) onActivePathChange(path);
    },
    [onActivePathChange, syncCardClasses]
  );

  return (
    <div
      className="present-tour"
      data-testid="present-tour"
      style={fontSize ? ({ '--diffs-font-size': `${fontSize}px` } as React.CSSProperties) : undefined}
    >
      {summary && (
        <div
          className={`present-tour-summary ${summaryVisible ? '' : 'collapsed'}`}
          data-testid="present-tour-summary"
          onWheel={handleSummaryWheel}
        >
          <button
            type="button"
            className="present-tour-summary-toggle"
            data-testid="present-tour-summary-toggle"
            aria-expanded={summaryVisible}
            onClick={() => onSummaryVisibleChange?.(!summaryVisible)}
          >
            <span className={`present-tour-summary-chevron${summaryVisible ? ' is-open' : ''}`} aria-hidden="true">
              ▸
            </span>
            Summary
          </button>
          <div
            className="present-tour-summary-body"
            data-testid="present-tour-summary-body"
            aria-hidden={!summaryVisible}
            ref={summaryBodyRef}
          >
            <Markdown>{summary}</Markdown>
          </div>
        </div>
      )}

      {!tourMounted ? (
        <div className="present-tour-loading">Loading tour…</div>
      ) : (
        <CodeView<AnnotationMeta>
          ref={handleRef}
          items={items}
          options={options}
          className="present-tour-scroller"
          style={{ flex: 1, minHeight: 0, overflow: 'auto' }}
          containerRef={containerRef}
          selectedLines={selectedLines}
          renderAnnotation={renderAnnotation}
          renderHeaderMetadata={renderHeaderMetadata}
          renderHeaderPrefix={renderHeaderPrefix}
          onScroll={handleScroll}
          disableWorkerPool
        />
      )}
    </div>
  );
}

export default PresentTour;
