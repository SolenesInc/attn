import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { SidebarProps } from './sidebarTypes';
import { useEscapeStack } from '../hooks/useEscapeStack';

export function SidebarSettings({
  queueModeEnabled,
  onToggleQueueMode,
  crewQueueEnabled,
  onToggleCrewQueue,
  harnessLogosEnabled,
  onToggleHarnessLogos,
  workspaceSelectionStyle,
  onWorkspaceSelectionStyleChange,
  showSessionless,
  onToggleShowSessionless,
  displayMode,
  setDisplayMode,
}: Pick<
  SidebarProps,
  | 'queueModeEnabled'
  | 'onToggleQueueMode'
  | 'crewQueueEnabled'
  | 'onToggleCrewQueue'
  | 'harnessLogosEnabled'
  | 'onToggleHarnessLogos'
  | 'workspaceSelectionStyle'
  | 'onWorkspaceSelectionStyleChange'
  | 'showSessionless'
  | 'onToggleShowSessionless'
> & {
  displayMode: 'open' | 'tight' | 'boxed';
  setDisplayMode: (mode: 'open' | 'tight' | 'boxed') => void;
}) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDialogElement>(null);
  useEscapeStack(() => {
    triggerRef.current?.focus();
    setSettingsOpen(false);
  }, settingsOpen);
  useLayoutEffect(() => {
    if (!settingsOpen) return;
    const dialog = dialogRef.current;
    dialog?.querySelector('button')?.focus();
  }, [settingsOpen]);
  useEffect(() => {
    if (!settingsOpen) return;
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !anchorRef.current?.contains(event.target))
        setSettingsOpen(false);
    };
    document.addEventListener('pointerdown', dismiss);
    return () => document.removeEventListener('pointerdown', dismiss);
  }, [settingsOpen]);
  return (
    <div className="sidebar-settings-anchor" ref={anchorRef}>
      <button
        ref={triggerRef}
        className={`sidebar-settings-btn ${settingsOpen ? 'active' : ''}`}
        onClick={() => setSettingsOpen((open) => !open)}
        title="Sidebar settings"
        aria-label="Sidebar settings"
        aria-expanded={settingsOpen}
      >
        <SettingsIcon />
      </button>
      {settingsOpen && (
        <dialog
          open
          ref={dialogRef}
          className="sidebar-settings-popover"
          aria-label="Sidebar settings"
        >
          <button
            type="button"
            className="sidebar-settings-switch-row sidebar-settings-switch-row--lead"
            role="switch"
            aria-checked={queueModeEnabled}
            data-testid="toggle-queue-mode"
            onClick={() => onToggleQueueMode?.()}
          >
            <span className="sidebar-settings-switch-label">Agent queue</span>
            <span
              className={`sidebar-settings-switch ${queueModeEnabled ? 'on' : ''}`}
              aria-hidden="true"
            />
          </button>
          <button
            type="button"
            className="sidebar-settings-switch-row"
            role="switch"
            aria-checked={crewQueueEnabled}
            data-testid="toggle-crew-queue"
            onClick={() => onToggleCrewQueue?.()}
          >
            <span className="sidebar-settings-switch-label">Crew in queue</span>
            <span
              className={`sidebar-settings-switch ${crewQueueEnabled ? 'on' : ''}`}
              aria-hidden="true"
            />
          </button>
          <button
            type="button"
            className="sidebar-settings-switch-row sidebar-settings-switch-row--adjacent"
            role="switch"
            aria-checked={harnessLogosEnabled}
            data-testid="toggle-harness-logos"
            onClick={() => onToggleHarnessLogos?.()}
          >
            <span className="sidebar-settings-switch-label">Harness logos</span>
            <span
              className={`sidebar-settings-switch ${harnessLogosEnabled ? 'on' : ''}`}
              aria-hidden="true"
            />
          </button>
          <span className="sidebar-settings-label">Display</span>
          <div className="sidebar-display-toggle" role="group" aria-label="Sidebar display">
            {(['open', 'tight', 'boxed'] as const).map((mode) => (
              <button
                key={mode}
                className={displayMode === mode ? 'active' : ''}
                onClick={() => {
                  setDisplayMode(mode);
                }}
              >
                {mode}
              </button>
            ))}
          </div>
          <span className="sidebar-settings-sub-label">Tile focus</span>
          <div
            className="sidebar-display-toggle sidebar-display-toggle--selection"
            role="group"
            aria-label="Tile focus style"
          >
            {(['dim', 'rail', 'spotlight'] as const).map((style) => (
              <button
                type="button"
                key={style}
                className={workspaceSelectionStyle === style ? 'active' : ''}
                aria-pressed={workspaceSelectionStyle === style}
                onClick={() => onWorkspaceSelectionStyleChange?.(style)}
              >
                {style}
              </button>
            ))}
          </div>
          <button
            type="button"
            className="sidebar-settings-switch-row"
            role="switch"
            aria-checked={showSessionless}
            data-testid="toggle-show-sessionless"
            onClick={() => onToggleShowSessionless?.()}
          >
            <span className="sidebar-settings-switch-label">Tile-only workspaces</span>
            <span
              className={`sidebar-settings-switch ${showSessionless ? 'on' : ''}`}
              aria-hidden="true"
            />
          </button>
        </dialog>
      )}
    </div>
  );
}

function SettingsIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M3 8h10M3 4.5h10M3 11.5h10"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
      <path
        d="M6 4.5h1.8M9.2 8H11M5.2 11.5H7"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinecap="round"
      />
    </svg>
  );
}
