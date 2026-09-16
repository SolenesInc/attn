import {
  AppInputsContext,
  AttentionContext,
  LibrariesContext,
  SessionsContext,
  ShellContext,
  WorkspacesContext,
} from './AppContexts';
import type { AppContentProps } from './appSupport';
import { AppSurface } from './AppSurface';
import { useAppController } from './useAppController';

export function AppContent(props: AppContentProps) {
  const app = useAppController(props);
  return (
    <AppInputsContext.Provider value={app.inputs}>
      <WorkspacesContext.Provider value={app.workspaces}>
        <SessionsContext.Provider value={app.sessions}>
          <AttentionContext.Provider value={app.attention}>
            <LibrariesContext.Provider value={app.libraries}>
              <ShellContext.Provider value={app.shell}>
                <AppSurface />
              </ShellContext.Provider>
            </LibrariesContext.Provider>
          </AttentionContext.Provider>
        </SessionsContext.Provider>
      </WorkspacesContext.Provider>
    </AppInputsContext.Provider>
  );
}
