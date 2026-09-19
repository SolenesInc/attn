import type { CSSProperties, ReactNode } from 'react';
import type { NormalizedPaneBounds } from '../../types/workspace';
import { useWorkspaceContext } from './WorkspaceContext';

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

export function SuspendedWorkspacePane({
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
  const { attentionViewport, focusLeaf } = useWorkspaceContext();
  const column = bounds.width * attentionViewport.width <= bounds.height * attentionViewport.height;
  return (
    <div
      className={`workspace-pane ${kind === 'tile' ? 'workspace-pane--tile' : ''} workspace-pane--suspended workspace-pane--suspended-${column ? 'column' : 'row'}`}
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
        className="workspace-suspended-leaf"
        aria-label={`Expand ${title}`}
        title={`Expand ${title}`}
        onMouseDown={(event) => event.stopPropagation()}
        onClick={() => focusLeaf(leafId)}
      >
        {children}
        <span className="workspace-suspended-label">{title}</span>
      </button>
    </div>
  );
}
