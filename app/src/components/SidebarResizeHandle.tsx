import { useLayoutEffect } from 'react';
import type { SidebarWidth } from '../application/useSidebarWidth';

export function SidebarResizeHandle({ width }: { width: SidebarWidth }) {
  const { cancelDrag } = width;
  useLayoutEffect(() => cancelDrag, [cancelDrag]);
  return (
    <div
      className="sidebar-resize-handle"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the sidebar"
      data-dragging={width.dragging ? '1' : undefined}
      onPointerDown={width.startDrag}
      onMouseDown={(event) => event.preventDefault()}
      onDoubleClick={width.clearWidth}
    >
      <div ref={width.labelRef} className="sidebar-resize-label" hidden={!width.dragging && !width.stepLabelShown} />
    </div>
  );
}
