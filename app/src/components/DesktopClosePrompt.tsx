import { ModalDialog } from './MigrationPicker/ModalDialog';
import './MigrationPicker/MigrationPicker.css';
import './DesktopClosePrompt.css';

interface Props {
  label: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function DesktopClosePrompt({ label, onConfirm, onCancel }: Props) {
  return (
    <ModalDialog className="desktop-close-prompt" labelledBy="desktop-close-title" onCancel={onCancel}>
      <div className="mp-dialog-top">
        <h2 id="desktop-close-title">Close {label}?</h2>
      </div>
      <div className="mp-dialog-body">
        <p>Close this desktop and everything inside it?</p>
      </div>
      <div className="mp-dialog-actions">
        <button type="button" className="mp-button" autoFocus onClick={onCancel}>Cancel</button>
        <button type="button" className="mp-button primary" onClick={onConfirm}>Close desktop</button>
      </div>
    </ModalDialog>
  );
}
