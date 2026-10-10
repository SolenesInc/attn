import { ModalDialog } from './MigrationPicker/ModalDialog';
import './MigrationPicker/MigrationPicker.css';
import './DesktopClosePrompt.css';

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
  const counts = [
    agents > 0 ? count(agents, 'agent') : '',
    shells > 0 ? count(shells, 'shell') : '',
    tiles > 0 ? count(tiles, 'tile') : '',
  ].filter(Boolean);
  const summary = counts.length === 1
    ? counts[0]
    : `${counts.slice(0, -1).join(', ')} and ${counts[counts.length - 1]}`;
  const ledgerSubject = agents > 0 ? (shells > 0 ? 'Agents and shells' : 'Agents') : 'Shells';
  const explanation = [
    agents > 0 || shells > 0 ? `${ledgerSubject} stay in the ledger for resuming.` : '',
    tiles > 0 ? 'Tiles are removed.' : '',
  ].filter(Boolean).join(' ');
  return (
    <ModalDialog className="desktop-close-prompt" labelledBy="desktop-close-title" onCancel={onCancel}>
      <div className="mp-dialog-top">
        <h2 id="desktop-close-title">Close {label}?</h2>
      </div>
      <div className="mp-dialog-body">
        <p>Close {summary} on this desktop?</p>
        <p>{explanation}</p>
      </div>
      <div className="mp-dialog-actions">
        <button type="button" className="mp-button" autoFocus onClick={onCancel}>Cancel</button>
        <button type="button" className="mp-button primary" onClick={onConfirm}>Close desktop</button>
      </div>
    </ModalDialog>
  );
}
