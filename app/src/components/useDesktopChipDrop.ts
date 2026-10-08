import { useSidebarContext } from './SidebarContext';
import type { SidebarDesktop } from './sidebarTypes';

export function useDesktopChipDrop() {
  const {
    leafDrag,
    dragHoverDesktopId,
    canAcceptLeafDrag,
    onDesktopDragEnter,
    onDesktopDragLeave,
    onDesktopDragDrop,
  } = useSidebarContext();
  return (desktop: SidebarDesktop) => {
    const accepts = canAcceptLeafDrag(desktop);
    let dropClass = '';
    if (leafDrag) {
      if (!accepts) dropClass = ' is-drop-disabled';
      else dropClass = dragHoverDesktopId === desktop.id ? ' is-drop-entering' : ' is-drop-target';
    }
    return {
      dropClass,
      dropHandlers: {
        onPointerEnter: () => {
          if (accepts) onDesktopDragEnter?.(desktop);
        },
        onPointerLeave: () => {
          if (accepts) onDesktopDragLeave?.(desktop);
        },
        onPointerUp: (event: React.PointerEvent) => {
          if (accepts) onDesktopDragDrop?.(desktop, event.altKey);
        },
      },
    };
  };
}
