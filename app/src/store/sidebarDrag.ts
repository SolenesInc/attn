import { create } from 'zustand';

export const useSidebarDrag = create<{ dragging: boolean }>(() => ({ dragging: false }));
