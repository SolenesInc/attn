import { createContext, useContext, type Context } from 'react';
import type { AppContentProps } from './appSupport';
import type { useAppController } from './useAppController';

function useRequiredContext<T>(context: Context<T | null>): T {
  const value = useContext(context);
  if (!value) throw new Error('App surfaces require AppContent');
  return value;
}

export const AppInputsContext = createContext<AppContentProps | null>(null);
export function useAppInputs() {
  return useRequiredContext(AppInputsContext);
}

export const DesktopsContext = createContext<
  ReturnType<typeof useAppController>['desktops'] | null
>(null);
export function useDesktopRuntimeContext() {
  return useRequiredContext(DesktopsContext).desktopRuntime;
}
export function useNavigationContext() {
  return useRequiredContext(DesktopsContext).navigation;
}
export function useDesktopNavigationContext() {
  const { desktopNavigation, desktopOverviewOpen, setDesktopOverviewOpen, profileSwitcherOpen, setProfileSwitcherOpen } =
    useRequiredContext(DesktopsContext);
  return { desktopNavigation, desktopOverviewOpen, setDesktopOverviewOpen, profileSwitcherOpen, setProfileSwitcherOpen };
}
export function useDesktopTilesContext() {
  return useRequiredContext(DesktopsContext).desktopTiles;
}
export function useDesktopResidencyContext() {
  return useRequiredContext(DesktopsContext).desktopResidency;
}
export function useLeafDragContext() {
  return useRequiredContext(DesktopsContext).leafDrag;
}

export const SessionsContext = createContext<
  ReturnType<typeof useAppController>['sessions'] | null
>(null);
export function useAppSessionsContext() {
  return useRequiredContext(SessionsContext).appSessions;
}
export function useSessionLaunchContext() {
  return useRequiredContext(SessionsContext).sessionLaunch;
}
export function usePRLauncherContext() {
  return useRequiredContext(SessionsContext).prLauncher;
}
export function useSessionLifecycleContext() {
  return useRequiredContext(SessionsContext).sessionLifecycle;
}
export function useChiefOfStaffContext() {
  return useRequiredContext(SessionsContext).chiefOfStaff;
}

export const AttentionContext = createContext<
  ReturnType<typeof useAppController>['attention'] | null
>(null);
export function useAttentionQueueContext() {
  return useRequiredContext(AttentionContext).attentionQueue;
}
export function useAppGridContext() {
  return useRequiredContext(AttentionContext).appGrid;
}

export const LibrariesContext = createContext<
  ReturnType<typeof useAppController>['libraries'] | null
>(null);
export function useWorkflowPanelContext() {
  return useRequiredContext(LibrariesContext).workflowPanel;
}
export function useAppGardenActionsContext() {
  return useRequiredContext(LibrariesContext).appGardenActions;
}
export function useAppNotebookSurfaceContext() {
  return useRequiredContext(LibrariesContext).appNotebookSurface;
}
export function useCrewPanelContext() {
  return useRequiredContext(LibrariesContext).crewPanel;
}

export const ShellContext = createContext<ReturnType<typeof useAppController>['shell'] | null>(
  null,
);
export function useAppShell() {
  return useRequiredContext(ShellContext).surface;
}
export function useAppAppearanceContext() {
  return useRequiredContext(ShellContext).appAppearance;
}
export function useAppPanelsContext() {
  return useRequiredContext(ShellContext).appPanels;
}
export function useAppErrorsContext() {
  return useRequiredContext(ShellContext).appErrors;
}
export function useAppDiagnosticsContext() {
  return useRequiredContext(ShellContext).appDiagnostics;
}
