import type { CSSProperties, ReactNode } from 'react';
import type { NormalizedPaneBounds } from '../../types/desktop';
import { useDesktopContext } from './DesktopContext';

interface Props {
  leafId: string;
  title: string;
  kind: 'agent' | 'tile';
  tileKind?: string;
  sessionId?: string;
  bounds: NormalizedPaneBounds;
  path: string;
  frameStyle: CSSProperties;
  children: ReactNode;
}

export function SuspendedDesktopPane({
  leafId,
  title,
  kind,
  tileKind,
  sessionId,
  bounds,
  path,
  frameStyle,
  children,
}: Props) {
  const { attentionViewport, focusLeaf } = useDesktopContext();
  const column = bounds.width * attentionViewport.width <= bounds.height * attentionViewport.height;
  return (
    <div
      className={`desktop-pane ${kind === 'tile' ? 'desktop-pane--tile' : ''} desktop-pane--suspended desktop-pane--suspended-${column ? 'column' : 'row'}`}
      data-pane-id={leafId}
      data-pane-kind={kind}
      data-tile-kind={tileKind}
      data-pane-session-id={sessionId}
      data-pane-path={path}
      data-pane-suspended="true"
      style={frameStyle}
    >
      <button
        type="button"
        className="desktop-suspended-leaf"
        aria-label={`Expand ${title}`}
        title={`Expand ${title}`}
        onMouseDown={(event) => event.stopPropagation()}
        onClick={() => focusLeaf(leafId)}
      >
        {children}
        <span className="desktop-suspended-label">{title}</span>
      </button>
    </div>
  );
}
