// Reproduces the DOM shape SessionTerminalDesktop renders for a multi-leaf
// desktop, loading the real stylesheet so the actual cascade is under test.
import { useEffect } from 'react';
import '../../src/components/SessionTerminalDesktop/SessionTerminalDesktop.css';
import type { HarnessProps } from '../types';

export function PaneFocusRingHarness({ onReady, setTriggerRerender }: HarnessProps) {
  useEffect(() => {
    setTriggerRerender(() => () => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div
      className="session-terminal-desktop desktop-selection--rail multi-leaf"
      data-testid="desktop"
      style={{ position: 'relative', width: 400, height: 300 }}
    >
      <div
        className="desktop-pane active"
        data-testid="pane-active"
        style={{ position: 'absolute', inset: '0 50% 0 0' }}
      >
        <div className="desktop-pane-header desktop-pane-header--draggable">
          <span className="desktop-pane-title">shell</span>
        </div>
        <div className="desktop-pane-body" />
      </div>
      <div
        className="desktop-split-divider desktop-split-divider--vertical"
        data-testid="split-divider"
        style={{ left: '50%', top: 0, bottom: 0, background: 'black' }}
      />
      <div
        className="desktop-pane desktop-pane--tile"
        data-testid="tile-inactive"
        style={{ position: 'absolute', inset: '0 0 0 50%' }}
      >
        <div className="desktop-dock-tile-header">
          <span className="desktop-dock-tile-title">notes.md</span>
        </div>
      </div>
    </div>
  );
}
