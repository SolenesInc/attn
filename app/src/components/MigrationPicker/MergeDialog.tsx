import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { DraftPreview } from './DraftPreview';
import { planGroupIds, planJoin, type DraftDesktopView, type DropEdge, type GroupView, type PlanNode } from './migrationDraft';

const EDGES: Array<[DropEdge, string]> = [
  ['left', '← Left'],
  ['right', 'Right →'],
  ['top', '↑ Above'],
  ['bottom', 'Below ↓'],
];

const EDGE_KEYS: Record<string, DropEdge> = {
  ArrowLeft: 'left',
  ArrowRight: 'right',
  ArrowUp: 'top',
  ArrowDown: 'bottom',
};

export interface MergeChoice {
  anchorGroupId: string | null;
  edge: DropEdge;
  share: number;
}

interface MergeDialogProps {
  moving: GroupView;
  target: DraftDesktopView;
  remaining: PlanNode;
  groupById: Map<string, GroupView>;
  draftChanged: boolean;
  onConfirm: (choice: MergeChoice) => void;
  onCancel: () => void;
}

export function MergeDialog({ moving, target, remaining, groupById, draftChanged, onConfirm, onCancel }: MergeDialogProps) {
  const [anchorGroupId, setAnchorGroupId] = useState<string | null>(null);
  const [edge, setEdge] = useState<DropEdge>('right');
  const [percent, setPercent] = useState(50);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const movingId = moving.group.group_id;
  const preview = planJoin(remaining, movingId, anchorGroupId, edge, percent / 100);

  useEscapeStack(onCancel, true);
  useEffect(() => {
    confirmRef.current?.focus();
  }, []);

  const onKeyDown = (event: KeyboardEvent<HTMLDialogElement>) => {
    const tag = (event.target as HTMLElement).tagName;
    if (tag === 'INPUT' || tag === 'SELECT') return;
    const next = EDGE_KEYS[event.key];
    if (!next) return;
    event.preventDefault();
    setEdge(next);
    confirmRef.current?.focus();
  };

  return (
    <div className="mp-scrim">
      <dialog
        open
        className="mp-dialog"
        aria-modal="true"
        aria-labelledby="mp-merge-title"
        onKeyDown={onKeyDown}
      >
        <div className="mp-dialog-top">
          <div>
            <div className="mp-eyebrow">{target.label} · add a split</div>
            <h2 id="mp-merge-title">Merge {moving.group.title}</h2>
            <p>Choose a side. The splits inside each workspace stay as they are.</p>
          </div>
          <button type="button" className="mp-button quiet small" aria-label="Cancel merge" onClick={onCancel}>×</button>
        </div>
        <div className="mp-dialog-body">
          {draftChanged && (
            <div className="mp-notice info">The draft changed in another window. Merging re-checks it before anything applies.</div>
          )}
          <label className="mp-join-config">
            <span>Place beside</span>
            <select
              value={anchorGroupId ?? ''}
              onChange={(event) => setAnchorGroupId(event.target.value || null)}
            >
              <option value="">Entire desktop</option>
              {planGroupIds(remaining).map((id) => (
                <option key={id} value={id}>{groupById.get(id)?.group.title ?? id}</option>
              ))}
            </select>
          </label>
          <div className="mp-edges" role="group" aria-label="Split side">
            {EDGES.map(([value, label]) => (
              <button
                key={value}
                type="button"
                className={`mp-button${edge === value ? ' chosen' : ''}`}
                aria-pressed={edge === value}
                onClick={() => setEdge(value)}
              >
                {label}
              </button>
            ))}
          </div>
          <div className="mp-join-preview">
            <DraftPreview tree={preview} groupById={groupById} highlightId={movingId} />
          </div>
          <div className="mp-join-ratio">
            <label htmlFor="mp-merge-share">Merged workspace share</label>
            <input
              id="mp-merge-share"
              type="range"
              min={1}
              max={99}
              value={percent}
              onChange={(event) => setPercent(Number(event.target.value))}
            />
            <output htmlFor="mp-merge-share">{percent}%</output>
          </div>
          <p className="mp-join-hint">
            The outline marks the workspace you’re merging. Arrow keys choose a side; Enter merges it. Dividers stay adjustable after migration.
          </p>
          <div className="mp-dialog-actions">
            <button type="button" className="mp-button quiet" onClick={onCancel}>Cancel</button>
            <button
              type="button"
              ref={confirmRef}
              className="mp-button primary"
              onClick={() => onConfirm({ anchorGroupId, edge, share: percent / 100 })}
            >
              Merge into {target.label} →
            </button>
          </div>
        </div>
      </dialog>
    </div>
  );
}
