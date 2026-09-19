import { createContext, useContext } from 'react';
import type { useWorkspaceController } from './useWorkspaceController';

export const WorkspaceContext = createContext<ReturnType<typeof useWorkspaceController> | null>(
  null,
);
export function useWorkspaceContext() {
  const value = useContext(WorkspaceContext);
  if (!value) throw new Error('Workspace surface requires WorkspaceContext');
  return value;
}
