import { createContext, useContext, type ReactNode } from 'react';
import type { CaptureHostState } from './client';

interface Controller { state: CaptureHostState; setBinding: (binding: string | null) => Promise<void> }
const Context = createContext<Controller | null>(null);
export function CaptureShortcutProvider({ value, children }: { value: Controller; children: ReactNode }) {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useCaptureShortcut() { return useContext(Context); }
