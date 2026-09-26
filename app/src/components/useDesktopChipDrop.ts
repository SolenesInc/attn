import { useSidebarContext } from './SidebarContext';
import type { SidebarWorkspace } from './sidebarTypes';

export function useDesktopChipDrop() {
  const {
    leafDrag,
    dragHoverWorkspaceId,
    canAcceptLeafDrag,
    onWorkspaceDragEnter,
    onWorkspaceDragLeave,
    onWorkspaceDragDrop,
  } = useSidebarContext();
  return (workspace: SidebarWorkspace) => {
    const accepts = canAcceptLeafDrag(workspace);
    let dropClass = '';
    if (leafDrag) {
      if (!accepts) dropClass = ' is-drop-disabled';
      else dropClass = dragHoverWorkspaceId === workspace.id ? ' is-drop-entering' : ' is-drop-target';
    }
    return {
      dropClass,
      dropHandlers: {
        onPointerEnter: () => {
          if (accepts) onWorkspaceDragEnter?.(workspace);
        },
        onPointerLeave: () => {
          if (accepts) onWorkspaceDragLeave?.(workspace);
        },
        onPointerUp: () => {
          if (accepts) onWorkspaceDragDrop?.(workspace);
        },
      },
    };
  };
}
