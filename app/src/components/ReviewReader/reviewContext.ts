import { createContext, useContext } from 'react';
import type { ReviewBundle, ReviewRow } from '../../types/generated';

export interface FileTarget {
  path: string;
  line?: number;
  range?: [number, number];
}

export interface StopExpansion {
  rows: ReviewRow[];
  error?: string;
}

export interface ReviewReaderContextValue {
  bundle: ReviewBundle;
  expansions: ReadonlyMap<number, StopExpansion>;
  openFile(target: FileTarget, options?: { fill?: boolean }): void;
  openSearch(query?: string): void;
}

export const ReviewReaderContext = createContext<ReviewReaderContextValue | null>(null);

export function useReviewReader(): ReviewReaderContextValue | null {
  return useContext(ReviewReaderContext);
}

export const CURSOR_CLASS = 'review-cursor';
