import { AppContext } from './AppContext';
import type { AppContentProps } from './appSupport';
import { AppSurface } from './AppSurface';
import { useAppController } from './useAppController';
export function AppContent(props: AppContentProps) {
  const app = useAppController(props);
  return (
    <AppContext.Provider value={app}>
      <AppSurface />
    </AppContext.Provider>
  );
}
