import './SessionPriority.css';

export function SessionPriority({ priority }: { priority?: boolean }) {
  return priority ? <span className="session-priority" role="img" aria-label="Priority" title="Priority">⚑</span> : null;
}
