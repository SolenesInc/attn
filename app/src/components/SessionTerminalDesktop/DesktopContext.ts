import { createContext, useContext } from 'react';
import type { useDesktopController } from './useDesktopController';

export const DesktopContext = createContext<ReturnType<typeof useDesktopController> | null>(
  null,
);
export function useDesktopContext() {
  const value = useContext(DesktopContext);
  if (!value) throw new Error('Desktop surface requires DesktopContext');
  return value;
}
