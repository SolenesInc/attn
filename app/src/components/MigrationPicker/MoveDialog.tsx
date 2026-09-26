import { useEffect, useRef } from 'react';
import { ModalDialog } from './ModalDialog';
import type { DraftDesktopView, GroupView } from './migrationDraft';

interface MoveDialogProps {
  moving: GroupView;
  desktops: DraftDesktopView[];
  currentKey: string | undefined;
  groupById: Map<string, GroupView>;
  onPick: (desktop: DraftDesktopView) => void;
  onCancel: () => void;
}

export function MoveDialog({ moving, desktops, currentKey, groupById, onPick, onCancel }: MoveDialogProps) {
  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    listRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
  }, []);

  return (
    <ModalDialog labelledBy="mp-move-title" onCancel={onCancel}>
        <div className="mp-dialog-top">
          <div>
            <div className="mp-eyebrow">Move</div>
            <h2 id="mp-move-title">Move {moving.group.title} to…</h2>
            <p>Pick any desktop, including the extras.</p>
          </div>
          <button type="button" className="mp-button quiet small" aria-label="Cancel move" onClick={onCancel}>×</button>
        </div>
        <div className="mp-dialog-body">
          <div className="mp-move-list" ref={listRef}>
            {desktops.map((desktop) => (
              <button
                key={desktop.desktop.key}
                type="button"
                className="mp-button"
                disabled={desktop.desktop.key === currentKey}
                onClick={() => onPick(desktop)}
              >
                {desktop.desktop.shortcut_slot ? <kbd>{desktop.desktop.shortcut_slot}</kbd> : null}
                {desktop.label}
                <span className="mp-move-names">
                  {desktop.groupIds.map((id) => groupById.get(id)?.group.title ?? id).join(', ') || 'empty'}
                </span>
              </button>
            ))}
          </div>
        </div>
    </ModalDialog>
  );
}
