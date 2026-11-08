import { formatShortcut } from '../../shortcuts/formatShortcut';

export function PopOutButton({ leafId, popped, className, onToggle }: { leafId: string; popped: boolean; className: string; onToggle: () => void }) {
  const label = popped ? 'Bring back tile' : 'Pop out tile';
  return (
    <button
      type="button"
      className={className}
      data-testid={`tile-window-toggle-${leafId}`}
      data-popped={popped || undefined}
      title={`${label} (${formatShortcut('tile.popOut')})`}
      aria-label={label}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onToggle();
      }}
    >
      <svg viewBox="0 0 16 16" aria-hidden="true">
        {popped
          ? <path d="M6.5 13.5h-4v-4M2.5 13.5L8 8M9.5 2.5h4v10h-10" fill="none" stroke="currentColor" strokeWidth="1.3" />
          : <path d="M9.5 2.5h4v4M13.5 2.5L8 8M6.5 3.5h-4v10h10v-4" fill="none" stroke="currentColor" strokeWidth="1.3" />}
      </svg>
    </button>
  );
}
