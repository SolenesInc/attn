import {
  AppInputsContext,
  AttentionContext,
  LibrariesContext,
  SessionsContext,
  ShellContext,
  DesktopsContext,
} from './AppContexts';
import type { AppContentProps } from './appSupport';
import { AppSurface } from './AppSurface';
import { useAppController } from './useAppController';

export function AppContent(props: AppContentProps) {
  const app = useAppController(props);
  return (
    <AppInputsContext.Provider value={app.inputs}>
      <DesktopsContext.Provider value={app.desktops}>
        <SessionsContext.Provider value={app.sessions}>
          <AttentionContext.Provider value={app.attention}>
            <LibrariesContext.Provider value={app.libraries}>
              <ShellContext.Provider value={app.shell}>
                <AppSurface />
              </ShellContext.Provider>
            </LibrariesContext.Provider>
          </AttentionContext.Provider>
        </SessionsContext.Provider>
      </DesktopsContext.Provider>
    </AppInputsContext.Provider>
  );
}
