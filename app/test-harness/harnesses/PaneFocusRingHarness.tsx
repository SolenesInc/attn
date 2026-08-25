// Reproduces the DOM shape SessionTerminalWorkspace renders for a multi-leaf
// workspace, loading the real stylesheet so the actual cascade is under test.
import { useEffect } from 'react';
import '../../src/components/SessionTerminalWorkspace/SessionTerminalWorkspace.css';
import type { HarnessProps } from '../types';

export function PaneFocusRingHarness({ onReady, setTriggerRerender }: HarnessProps) {
  useEffect(() => {
    setTriggerRerender(() => () => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div
      className="session-terminal-workspace workspace-selection--rail multi-leaf"
      data-testid="workspace"
      style={{ position: 'relative', width: 400, height: 300 }}
    >
      <div
        className="workspace-pane active"
        data-testid="pane-active"
        style={{ position: 'absolute', inset: '0 50% 0 0' }}
      >
        <div className="workspace-pane-header workspace-pane-header--draggable">
          <span className="workspace-pane-title">shell</span>
        </div>
        <div className="workspace-pane-body" />
      </div>
      <div
        className="workspace-split-divider workspace-split-divider--vertical"
        data-testid="split-divider"
        style={{ left: '50%', top: 0, bottom: 0, background: 'black' }}
      />
      <div
        className="workspace-pane workspace-pane--tile"
        data-testid="tile-inactive"
        style={{ position: 'absolute', inset: '0 0 0 50%' }}
      >
        <div className="workspace-dock-tile-header">
          <span className="workspace-dock-tile-title">notes.md</span>
        </div>
      </div>
    </div>
  );
}
