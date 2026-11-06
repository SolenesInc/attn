import { create } from 'zustand';

export interface ToastRow {
  id: string;
  message: string;
  source?: string;
  actionLabel?: string;
  sessionId?: string;
  action?: () => void | Promise<unknown>;
  completed?: boolean;
}

interface ToastState {
  rows: ToastRow[];
  fading: boolean;
  append: (row: Omit<ToastRow, 'id'> & { id?: string }) => void;
  complete: (id: string) => void;
  fade: () => void;
  clear: () => void;
}

const isNotice = (row: Omit<ToastRow, 'id'>) => !row.action && !row.sessionId;
const repeatsNotice = (existing: ToastRow, row: Omit<ToastRow, 'id'>) =>
  isNotice(existing) && isNotice(row) && existing.message === row.message && existing.source === row.source;

export const useToastStore = create<ToastState>((set) => ({
  rows: [],
  fading: false,
  append: (row) =>
    set((state) => ({
      rows: [
        ...(state.fading ? [] : state.rows).filter((existing) => existing.id !== row.id && !repeatsNotice(existing, row)),
        { ...row, id: row.id || crypto.randomUUID() },
      ],
      fading: false,
    })),
  complete: (id) =>
    set((state) => ({ rows: state.rows.map((row) => (row.id === id ? { ...row, completed: true } : row)) })),
  fade: () => set({ fading: true }),
  clear: () => set({ rows: [], fading: false }),
}));
