import { createContext, useContext } from 'react';
import type { ReviewBundle, ReviewRow, ReviewStop } from '../../types/generated';
import type { Annotation } from '../MarkdownReader/annotations/types';
import type { Frame, Side } from './frames';
import type { LabeledName, NavLine } from './navigation';

export interface FileTarget {
  path: string;
  line?: number;
  range?: [number, number];
  /** A file outside the review's repository that a navigation answer named; it opens read-only. */
  outside?: boolean;
}

/** The names labeled on one row while the user picks one: a document row by
 * its cursor key, or a file view row by the open file and its index. */
export interface RowLabels {
  at: { cursorKey: string } | { fileId: number; row: number };
  names: readonly LabeledName[];
}

/** Where the Walkthrough stands: the stop on the stage and the line the
 * file view's cursor is on. */
export interface StagePlace {
  stopLine: number;
  line?: number;
}

export interface StopExpansion {
  rows: ReviewRow[];
  extraLines: number;
  error?: string;
}

export function expansionKey(stop: ReviewStop): string {
  return `${stop.path}:${stop.from}-${stop.to}`;
}

export interface ReviewReaderContextValue {
  bundle: ReviewBundle;
  expansions: ReadonlyMap<string, StopExpansion>;
  frame: Frame;
  stage: StagePlace | null;
  showStop(stop: ReviewStop): void;
  openFile(target: FileTarget, options?: { fill?: boolean }): void;
  openSearch(query?: string): void;
  /** The user's marks on code, keyed by markKey(path, last line). */
  marksByLine: ReadonlyMap<string, Annotation[]>;
  /** What became of a question asked from this reader, by mark id. */
  asking: ReadonlyMap<string, string>;
  removeMark?: (id: string) => void;
  /** Opens a file of the review in the editor tile, when the review is on this machine. */
  openInEditor?: (path: string) => void;
  labels: RowLabels | null;
  /** The line a question waits on, until its answer, its error or Escape. */
  asked: NavLine['at'] | null;
  /** Whether the Brief lists the changed files under the change's facts. */
  filesShown: boolean;
  toggleFiles(): void;
  /** The side the drawn diagrams show, one for the Brief and the Map. */
  side: Side;
  /** Whether the document has a before and an after diagram, so b switches. */
  sides: boolean;
  switchSide(side: Side): void;
}

export const ReviewReaderContext = createContext<ReviewReaderContextValue | null>(null);

export function useReviewReader(): ReviewReaderContextValue | null {
  return useContext(ReviewReaderContext);
}

export const CURSOR_CLASS = 'review-cursor';
