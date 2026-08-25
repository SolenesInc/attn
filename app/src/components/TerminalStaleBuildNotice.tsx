import './TerminalStaleBuildNotice.css';

interface TerminalStaleBuildNoticeProps {
  onDismiss: () => void;
}

// Mirrors Sidebar.tsx and SessionActionsPopover.tsx, and moves when they move.
function ReloadHint() {
  return (
    <svg
      className="terminal-stale-build-notice-hint"
      viewBox="0 0 200 96"
      role="img"
      aria-label="The sidebar session row's ••• button opens a menu containing Reload session"
    >
      <rect className="hint-row" x="1" y="10" width="86" height="22" rx="5" />
      <circle className="hint-dot-state" cx="12" cy="21" r="3" />
      <rect className="hint-label" x="21" y="17" width="38" height="7" rx="3.5" />
      <text className="hint-more" x="76" y="25" textAnchor="middle">•••</text>

      <path className="hint-arrow" d="M76 34 L76 44 L104 44" />
      <path className="hint-arrow-head" d="M104 41 L110 44 L104 47 Z" />

      <rect className="hint-menu" x="110" y="30" width="89" height="64" rx="6" />
      <rect className="hint-item" x="116" y="36" width="46" height="6" rx="3" />
      <rect className="hint-item" x="116" y="49" width="58" height="6" rx="3" />
      <line className="hint-divider" x1="115" y1="61" x2="194" y2="61" />
      <rect className="hint-item-active-bg" x="113" y="64" width="83" height="15" rx="4" />
      <text className="hint-item-active" x="119" y="75">↻ Reload session</text>
      <rect className="hint-item" x="119" y="85" width="40" height="6" rx="3" />
    </svg>
  );
}

export function TerminalStaleBuildNotice({ onDismiss }: TerminalStaleBuildNoticeProps) {
  return (
    <div className="terminal-stale-build-notice" role="status" data-testid="terminal-stale-build-notice">
      <ReloadHint />
      <div className="terminal-stale-build-notice-body">
        <p className="terminal-stale-build-notice-lead">
          This session is running an older terminal.
        </p>
        <p>Reload it to bring it up to date.</p>
        <p className="terminal-stale-build-notice-aside">
          An update normally swaps this out on its own. This one could not.
        </p>
      </div>
      <button
        type="button"
        className="terminal-stale-build-notice-dismiss"
        onClick={onDismiss}
        data-testid="terminal-stale-build-notice-dismiss"
        aria-label="Dismiss older-terminal notice"
      >
        ×
      </button>
    </div>
  );
}
