import { invoke, isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { useLayoutEffect } from 'react';
import { isMacLikePlatform } from '../shortcuts/platform';
import { QueueBarInTitleBar } from './QueueBar';
import { DesktopShelf, useDesktopHasShelf } from './SessionTerminalDesktop/DesktopShelf';
import { useSidebarContext } from './SidebarContext';
import {
  CommandsButton,
  FlowToggleButton,
  InstanceMarker,
  NewAgentButton,
  ProfilePill,
  SidebarPanelButton,
  SidebarToggleButton,
} from './SidebarHeader';
import './TitleBar.css';

export const TITLE_BAR_HEIGHT_PX = 32;
const TRAFFIC_LIGHTS_PX = 78;
const NO_LIGHTS_PX = 8;

function useWindowTitleBar(overlay: boolean) {
  useLayoutEffect(() => {
    const root = document.documentElement.style;
    const showLightsSpace = (shown: boolean) =>
      root.setProperty('--title-bar-lights', `${shown ? TRAFFIC_LIGHTS_PX : NO_LIGHTS_PX}px`);
    if (overlay) {
      root.setProperty('--title-bar-height', `${TITLE_BAR_HEIGHT_PX}px`);
      showLightsSpace(isMacLikePlatform());
    } else {
      root.removeProperty('--title-bar-height');
      root.removeProperty('--title-bar-lights');
    }
    if (!isTauri()) return;
    void invoke('set_title_bar_overlay', { overlay }).catch((error) => {
      console.warn('[TitleBar] Failed to change the window title bar:', error);
    });
    if (!overlay || !isMacLikePlatform()) return;
    let stopped = false;
    const appWindow = getCurrentWindow();
    const syncFullscreen = () => {
      void appWindow.isFullscreen().then((fullscreen) => {
        if (!stopped) showLightsSpace(!fullscreen);
      });
    };
    syncFullscreen();
    const unlisten = appWindow.onResized(syncFullscreen);
    return () => {
      stopped = true;
      void unlisten.then((stop) => stop());
    };
  }, [overlay]);
}

function ShelfOfShownDesktop() {
  const { selectedDesktopId, homeActive } = useSidebarContext();
  const hasShelf = useDesktopHasShelf(selectedDesktopId);
  return hasShelf && selectedDesktopId && !homeActive ? <DesktopShelf desktopId={selectedDesktopId} /> : <span className="queue-bar-spacer" />;
}

export function TitleBar() {
  const { oneToolbar } = useSidebarContext();
  useWindowTitleBar(oneToolbar);
  return oneToolbar ? <TitleBarBand /> : null;
}

function TitleBarBand() {
  const { queue, collapsed, headerActions, windowCovered } = useSidebarContext();
  const notifications = headerActions.filter((action) => action.id === 'notifications');
  const others = headerActions.filter((action) => action.id !== 'notifications');
  return (
    <div className={`title-bar${windowCovered ? ' title-bar--covered' : ''}`} data-testid="title-bar" data-tauri-drag-region="deep">
      <div className={`title-bar-left${collapsed ? '' : ' title-bar-left--beside-sidebar'}`} data-testid="title-bar-left">
        <NewAgentButton />
        <CommandsButton />
        <FlowToggleButton />
        <SidebarToggleButton />
        {notifications.map((action) => <SidebarPanelButton key={action.id} action={action} />)}
      </div>
      {queue && collapsed ? (
        <QueueBarInTitleBar shelf={<ShelfOfShownDesktop />} />
      ) : (
        <div className="title-bar-middle"><ShelfOfShownDesktop /></div>
      )}
      <div className="title-bar-right" data-testid="title-bar-right">
        {others.map((action) => <SidebarPanelButton key={action.id} action={action} />)}
        <InstanceMarker />
        <ProfilePill />
      </div>
    </div>
  );
}
