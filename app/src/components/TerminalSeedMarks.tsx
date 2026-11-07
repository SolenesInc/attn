import { forwardRef, useImperativeHandle, useState } from 'react';
import type { TerminalSeedSegment } from '../utils/terminalSeedLinks';

export interface SeedMarkLayout {
  signature: string;
  canvasLeft: number;
  canvasTop: number;
  cellWidth: number;
  cellHeight: number;
  segments: TerminalSeedSegment[];
}

export interface TerminalSeedMarksHandle {
  setLayout: (layout: SeedMarkLayout | null) => void;
}

export const TerminalSeedMarks = forwardRef<TerminalSeedMarksHandle, { previewingSeedId: string | null }>(
  function TerminalSeedMarks({ previewingSeedId }, ref) {
    const [layout, setLayout] = useState<SeedMarkLayout | null>(null);
    useImperativeHandle(ref, () => ({
      setLayout: (next) => setLayout((current) => (current?.signature === next?.signature ? current : next)),
    }), []);
    if (!layout) return null;
    return (
      <div className="ghostty-terminal-seed-marks" aria-hidden="true">
        {layout.segments.map((segment, index) => (
          <span
            key={`${segment.seedId}:${segment.row}:${segment.startCol}:${index}`}
            className={`ghostty-terminal-seed-mark${previewingSeedId === segment.seedId ? ' is-previewing' : ''}`}
            data-terminal-seed-id={segment.seedId}
            style={{
              left: layout.canvasLeft + segment.startCol * layout.cellWidth,
              top: layout.canvasTop + segment.row * layout.cellHeight,
              width: (segment.endCol - segment.startCol) * layout.cellWidth,
              height: layout.cellHeight,
            }}
          />
        ))}
      </div>
    );
  },
);
