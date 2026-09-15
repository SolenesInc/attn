import { createContext, useContext } from 'react';
import type { useAppController } from './useAppController';
export const AppContext = createContext<ReturnType<typeof useAppController> | null>(null);
export function useAppContext() {
  const value = useContext(AppContext);
  if (!value) throw new Error('App surfaces require AppContext');
  return value;
}
