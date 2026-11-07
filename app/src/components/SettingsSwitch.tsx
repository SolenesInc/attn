export function SettingsSwitch({ on, label, onToggle, showState = false, describedBy, testID }: {
  on: boolean;
  label: string;
  onToggle: () => void;
  showState?: boolean;
  describedBy?: string;
  testID?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      aria-describedby={describedBy}
      data-testid={testID}
      className="settings-switch"
      onClick={onToggle}
    >
      <span className="settings-switch-track" />
      {showState && <span>{on ? 'On' : 'Off'}</span>}
    </button>
  );
}
