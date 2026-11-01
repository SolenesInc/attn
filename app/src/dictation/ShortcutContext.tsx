import { createContext, useContext, type ReactNode } from 'react';
import type { useDictationShortcut } from './speechHelper';

type Controller = ReturnType<typeof useDictationShortcut>;
const Context = createContext<Controller | null>(null);
export function DictationShortcutProvider({ value, children }: { value: Controller; children: ReactNode }) {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useDictationShortcutController() { return useContext(Context); }
