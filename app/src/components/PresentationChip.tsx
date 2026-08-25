import './PresentationChip.css';
import type { Presentation } from '../types/generated';

export function HeaderPresentationChip({
  presentation,
  onOpen,
}: {
  presentation: Presentation;
  onOpen: (presentationId: string) => void;
}) {
  return (
    <button
      type="button"
      className="presentation-chip"
      data-presentation-id={presentation.id}
      data-session-id={presentation.session_id}
      // In a split the pane header is a leaf-drag handle, so a sloppy click that drifts >=4px would relocate the pane.
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onOpen(presentation.id);
      }}
      title={presentation.title}
    >
      <span className="presentation-chip-dot" aria-hidden="true" />
      <span className="presentation-chip-label">▶ review</span>
    </button>
  );
}
