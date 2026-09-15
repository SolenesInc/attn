import { RenamePopover } from '../RenamePopover';
import { useWorkspaceContext } from './WorkspaceContext';
export function WorkspaceRenamePopover() {
  const { onRenameSession, renamePane, setRenamePane } = useWorkspaceContext();

  return (
    <>
      {renamePane && onRenameSession && (
        <RenamePopover
          key={renamePane.sessionId}
          initialValue={renamePane.name}
          label="Rename session"
          anchor={renamePane.anchor}
          onSubmit={(value) => onRenameSession(renamePane.sessionId, value)}
          onClose={() => setRenamePane(null)}
        />
      )}
    </>
  );
}
