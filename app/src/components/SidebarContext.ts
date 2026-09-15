import { createContext, useContext } from 'react';
import type { useSidebarState } from './useSidebarState';

export const SidebarContext = createContext<ReturnType<typeof useSidebarState> | null>(null);

export function useSidebarContext() {
  const state = useContext(SidebarContext);
  if (!state) throw new Error('Sidebar components require SidebarContext');
  return state;
}
