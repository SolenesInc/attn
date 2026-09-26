import type { CSSProperties } from 'react';
import { planBoxes, type Box, type GroupView, type PlanNode } from './migrationDraft';

function boxStyle(box: Box): CSSProperties {
  return {
    left: `${box.left * 100}%`,
    top: `${box.top * 100}%`,
    width: `${box.width * 100}%`,
    height: `${box.height * 100}%`,
  };
}

interface DraftPreviewProps {
  tree: PlanNode | null;
  groupById: Map<string, GroupView>;
  selectedId?: string | null;
  highlightId?: string | null;
  draggingId?: string | null;
  draggable?: boolean;
}

export function DraftPreview({ tree, groupById, selectedId, highlightId, draggingId, draggable }: DraftPreviewProps) {
  if (!tree) {
    return (
      <div className="mp-preview mp-preview-empty">
        <span className="mp-plus" aria-hidden="true">+</span> Empty
      </div>
    );
  }
  return (
    <div className="mp-preview">
      {[...planBoxes(tree)].map(([groupId, box]) => {
        const view = groupById.get(groupId);
        if (!view) return null;
        const confirmed = view.group.confirmed;
        const classes = [
          'mp-group',
          confirmed ? 'confirmed' : 'todo',
          groupId === selectedId ? 'selected' : '',
          groupId === highlightId ? 'highlight' : '',
          groupId === draggingId ? 'drag-source' : '',
        ].filter(Boolean).join(' ');
        return (
          <div
            key={groupId}
            className={classes}
            style={boxStyle(box)}
            data-migration-group={groupId}
            {...(draggable ? { 'data-drag-group': groupId } : {})}
            title={`${view.group.title}${confirmed ? ' · confirmed' : ' · needs your confirmation'}`}
          >
            {view.leaves.map((leaf) => (
              <div
                key={leaf.id}
                className={`mp-leaf${leaf.tile ? ' tile' : ''}${leaf.status === 'failed' ? ' failed' : ''}`}
                style={boxStyle(leaf)}
              >
                <span>{leaf.label}</span>
              </div>
            ))}
            <span className={confirmed ? 'mp-group-check' : 'mp-group-todo'} aria-hidden="true">
              {confirmed ? '✓' : '?'}
            </span>
          </div>
        );
      })}
    </div>
  );
}
