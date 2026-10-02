import { create } from 'zustand';

export interface ToastRow {
  id: string;
  message: string;
  source: string;
  tone: 'error' | 'notice';
  actionLabel?: string;
  sessionId?: string;
  launchKind?: 'crew' | 'automation';
  desktopLabel?: string;
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

export const useToastStore = create<ToastState>((set) => ({
  rows: [],
  fading: false,
  append: (row) =>
    set((state) => ({
      rows: [
        ...(state.fading ? [] : state.rows).filter((existing) => existing.id !== row.id),
        { ...row, id: row.id || crypto.randomUUID() },
      ],
      fading: false,
    })),
  complete: (id) =>
    set((state) => ({ rows: state.rows.map((row) => (row.id === id ? { ...row, completed: true } : row)) })),
  fade: () => set({ fading: true }),
  clear: () => set({ rows: [], fading: false }),
}));
