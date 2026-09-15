import { useWorkspaceContext } from './WorkspaceContext';
export function WorkspaceDragOverlays() {
  const { resizingSplit, effectiveDockTarget } = useWorkspaceContext();

  return (
    <>
      {effectiveDockTarget ? (
        <div
          className="workspace-dock-target"
          style={{
            left: `${effectiveDockTarget.rect.left * 100}%`,
            top: `${effectiveDockTarget.rect.top * 100}%`,
            width: `${effectiveDockTarget.rect.width * 100}%`,
            height: `${effectiveDockTarget.rect.height * 100}%`,
          }}
        />
      ) : null}
      {resizingSplit ? (
        <div
          className={`workspace-resize-shield workspace-resize-shield--${resizingSplit.direction}`}
          data-resizing-split-id={resizingSplit.splitId}
          onPointerDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
          onPointerMove={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
          onMouseDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
          onMouseMove={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
          onMouseUp={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
        />
      ) : null}
    </>
  );
}
