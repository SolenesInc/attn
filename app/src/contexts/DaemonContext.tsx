// app/src/contexts/DaemonContext.tsx
import { createContext, useContext, useMemo, ReactNode } from 'react';

interface PRActionResult {
  success: boolean;
  error?: string;
}

interface DaemonContextType {
  sendPRAction: (action: 'approve' | 'merge', id: string, method?: string) => Promise<PRActionResult>;
  sendMutePR: (prId: string) => void;
  sendMuteRepo: (repo: string) => void;
  sendMuteAuthor: (author: string) => void;
  sendPRVisited: (prId: string) => void;
}

const DaemonContext = createContext<DaemonContextType | null>(null);

export function DaemonProvider({
  children,
  sendPRAction,
  sendMutePR,
  sendMuteRepo,
  sendMuteAuthor,
  sendPRVisited,
}: {
  children: ReactNode;
  sendPRAction: DaemonContextType['sendPRAction'];
  sendMutePR: (prId: string) => void;
  sendMuteRepo: (repo: string) => void;
  sendMuteAuthor: (author: string) => void;
  sendPRVisited: (prId: string) => void;
}) {
  const value = useMemo(
    () => ({ sendPRAction, sendMutePR, sendMuteRepo, sendMuteAuthor, sendPRVisited }),
    [sendPRAction, sendMutePR, sendMuteRepo, sendMuteAuthor, sendPRVisited],
  );
  return <DaemonContext.Provider value={value}>{children}</DaemonContext.Provider>;
}

export function useDaemonContext() {
  const context = useContext(DaemonContext);
  if (!context) {
    throw new Error('useDaemonContext must be used within DaemonProvider');
  }
  return context;
}
