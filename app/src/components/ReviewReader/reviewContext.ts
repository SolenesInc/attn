import { createContext, useContext } from 'react';
import type { ReviewBundle, ReviewNote, ReviewRow, ReviewStop } from '../../types/generated';
import type { Annotation } from '../MarkdownReader/annotations/types';
import type { Frame } from './frames';

export interface FileTarget {
  path: string;
  line?: number;
  range?: [number, number];
  notes?: readonly ReviewNote[];
}

/** Where the Walkthrough stands: the stop on the stage and the line the
 * file view's cursor is on. */
export interface StagePlace {
  stopLine: number;
  line?: number;
}

export interface StopExpansion {
  rows: ReviewRow[];
  error?: string;
}

export interface ReviewReaderContextValue {
  bundle: ReviewBundle;
  expansions: ReadonlyMap<number, StopExpansion>;
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
}

export const ReviewReaderContext = createContext<ReviewReaderContextValue | null>(null);

export function useReviewReader(): ReviewReaderContextValue | null {
  return useContext(ReviewReaderContext);
}

export const CURSOR_CLASS = 'review-cursor';
