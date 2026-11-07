import { formatShortcut } from '../../shortcuts/formatShortcut';

export function ReturnToRowButton({ onReturn }: { onReturn: () => void }) {
  return (
    <button
      type="button"
      className="desktop-return-to-row"
      data-fit-fixed
      title={`Return to row (${formatShortcut('terminal.toggleMaximize')})`}
      aria-label="Return to row"
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onReturn();
      }}
    >
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M2.5 6H6V2.5M13.5 6H10V2.5M2.5 10H6v3.5M13.5 10H10v3.5" />
      </svg>
    </button>
  );
}
