import { ModalDialog } from './MigrationPicker/ModalDialog';
import './MigrationPicker/MigrationPicker.css';

interface Props {
  label: string;
  agents: number;
  shells: number;
  tiles: number;
  onConfirm: () => void;
  onCancel: () => void;
}

export function DesktopClosePrompt({ label, agents, shells, tiles, onConfirm, onCancel }: Props) {
  const count = (value: number, noun: string) => `${value} ${noun}${value === 1 ? '' : 's'}`;
  return (
    <ModalDialog labelledBy="desktop-close-title" onCancel={onCancel}>
      <div className="mp-dialog-top">
        <h2 id="desktop-close-title">Close {label}?</h2>
      </div>
      <div className="mp-dialog-body">
        <p>Close {count(agents, 'agent')}, {count(shells, 'shell')} and {count(tiles, 'tile')} on this desktop?</p>
        <p>Agents and shells stay in the ledger for resuming. Tiles are removed.</p>
      </div>
      <div className="mp-dialog-actions">
        <button type="button" className="mp-button" autoFocus onClick={onCancel}>Cancel</button>
        <button type="button" className="mp-button primary" onClick={onConfirm}>Close desktop</button>
      </div>
    </ModalDialog>
  );
}
