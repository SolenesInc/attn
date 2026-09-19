import './SessionTerminalWorkspace.css';
import { forwardRef } from 'react';
import { WorkspaceContext } from './WorkspaceContext';
import { WorkspaceSurface } from './WorkspaceSurface';
import { useWorkspaceController } from './useWorkspaceController';
import type {
  SessionTerminalWorkspaceHandle,
  SessionTerminalWorkspaceProps,
} from './workspaceTypes';
export type {
  SessionTerminalWorkspaceHandle,
  SessionTerminalWorkspaceProps,
} from './workspaceTypes';

export const SessionTerminalWorkspace = forwardRef<
  SessionTerminalWorkspaceHandle,
  SessionTerminalWorkspaceProps
>(function SessionTerminalWorkspace(props, ref) {
  const state = useWorkspaceController(props, ref);
  return (
    <WorkspaceContext.Provider value={state}>
      <WorkspaceSurface />
    </WorkspaceContext.Provider>
  );
});
