import { RenamePopover } from '../RenamePopover';
import { useDesktopContext } from './DesktopContext';
export function DesktopRenamePopover() {
  const { onRenameSession, renamePane, setRenamePane } = useDesktopContext();

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
