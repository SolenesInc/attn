import './PaneSeedChip.css';
import type { Seed } from '../hooks/useDaemonSocket';

// Guards the leaf-drag: onPointerDown stops propagation so a click that drifts >=4px
// cannot relocate the pane, and onClick stops the header from re-selecting it.
export function PaneSeedChip({
  seedId,
  seed,
  unread,
  sessionId,
  onOpen,
}: {
  seedId: string;
  seed?: Seed;
  unread: boolean;
  sessionId: string;
  onOpen: () => void;
}) {
  const title = seed?.title.trim();
  const label = title || seedId;
  return (
    <button
      type="button"
      className="pane-seed-chip"
      data-status={seed?.status}
      data-testid={`seed-chip-${sessionId}`}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onOpen();
      }}
      title={`${label} (${seedId}). Click to open the seed`}
    >
      <span className="pane-seed-chip-rule" aria-hidden="true" />
      <span className="pane-seed-chip-title">{label}</span>
      {title ? <span className="pane-seed-chip-id">{seedId}</span> : null}
      {unread ? (
        <span
          className="pane-seed-chip-unread"
          data-testid={`seed-chip-unread-${sessionId}`}
          aria-label="Unread activity"
        />
      ) : null}
    </button>
  );
}
