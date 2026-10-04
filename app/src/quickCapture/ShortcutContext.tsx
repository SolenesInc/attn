import { createContext, useContext, type ReactNode } from 'react';
import type { QuickCaptureHostState } from './client';

interface Controller { state: QuickCaptureHostState; setBinding: (binding: string | null) => Promise<void> }
const Context = createContext<Controller | null>(null);
export function QuickCaptureShortcutProvider({ value, children }: { value: Controller; children: ReactNode }) {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useCaptureShortcut() { return useContext(Context); }
