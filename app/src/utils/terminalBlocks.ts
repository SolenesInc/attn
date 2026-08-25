import type { Osc133Marker } from './terminalOsc133';

export interface BlockPosition {
  row: number;
  col: number;
}

export interface TerminalBlock {
  id: number;
  promptRow: number;
  inputStart?: BlockPosition;
  outputStartRow?: number;
  // Exclusive: the row the cursor was on when the command finished.
  endRow?: number;
  command: string;
  exitCode?: number;
  anchorRow: number;
  anchorText: string;
}

export interface BlockRowAccess {
  totalRows(): number;
  rowText(bufferRow: number): string;
}

const MAX_BLOCKS = 200;
const ANCHOR_LENGTH = 64;
export const REANCHOR_SCAN_ROWS = 64;
// Height-only resizes shift rows uniformly and can exceed the per-click window; a
// width change reflows non-uniformly and clears the store instead.
export const RESIZE_REANCHOR_SCAN_ROWS = 512;

interface PendingBlock {
  id: number;
  promptRow: number;
  inputStart?: BlockPosition;
  outputStartRow?: number;
  command?: string;
}

export class TerminalBlockStore {
  private completed: TerminalBlock[] = [];
  private pending: PendingBlock | null = null;
  private nextId = 1;

  applyMarker(marker: Osc133Marker, position: BlockPosition, rowTextAt?: (row: number) => string): void {
    // fish's `OSC 133;D` can go missing; close the open block here or two
    // commands silently merge into one.
    if (
      this.pending?.outputStartRow !== undefined
      && (marker.kind === 'prompt-start' || marker.kind === 'input-start' || marker.kind === 'pre-exec')
    ) {
      this.complete(this.pending, position.row, undefined, rowTextAt);
      this.pending = null;
    }

    switch (marker.kind) {
      case 'prompt-start':
        this.pending = { id: this.nextId, promptRow: position.row };
        this.nextId += 1;
        return;
      case 'input-start':
        if (!this.pending) this.pending = this.openPending(position.row);
        this.pending.inputStart = position;
        return;
      case 'pre-exec':
        if (!this.pending) this.pending = this.openPending(position.row);
        this.pending.outputStartRow = position.row;
        this.pending.command = marker.cmdline;
        return;
      case 'command-end': {
        const pending = this.pending;
        this.pending = null;
        if (pending && pending.outputStartRow !== undefined) {
          this.complete(pending, position.row, marker.exitCode, rowTextAt);
        }
        return;
      }
      default:
        return;
    }
  }

  private openPending(promptRow: number): PendingBlock {
    const pending = { id: this.nextId, promptRow };
    this.nextId += 1;
    return pending;
  }

  private complete(
    pending: PendingBlock,
    endRow: number,
    exitCode: number | undefined,
    rowTextAt?: (row: number) => string,
  ): void {
    if (pending.outputStartRow === undefined) return;
    const anchorRow = pending.inputStart?.row ?? pending.promptRow;
    this.completed.push({
      id: pending.id,
      promptRow: pending.promptRow,
      inputStart: pending.inputStart,
      outputStartRow: pending.outputStartRow,
      endRow,
      command: pending.command ?? '',
      exitCode,
      anchorRow,
      anchorText: (rowTextAt?.(anchorRow) ?? '').slice(0, ANCHOR_LENGTH),
    });
    if (this.completed.length > MAX_BLOCKS) {
      this.completed.splice(0, this.completed.length - MAX_BLOCKS);
    }
  }

  blocks(): readonly TerminalBlock[] {
    return this.completed;
  }

  hasBlocks(): boolean {
    return this.completed.length > 0;
  }

  blockAt(bufferRow: number): TerminalBlock | null {
    for (let i = this.completed.length - 1; i >= 0; i -= 1) {
      const block = this.completed[i];
      if (bufferRow >= block.promptRow && block.endRow !== undefined && bufferRow < block.endRow) {
        return block;
      }
    }
    return null;
  }

  blockAtAnchored(bufferRow: number, access: BlockRowAccess): TerminalBlock | null {
    for (let i = this.completed.length - 1; i >= 0; i -= 1) {
      const block = this.completed[i];
      if (block.endRow === undefined) continue;
      const delta = reanchorDelta(block, access);
      if (delta === null) continue;
      if (bufferRow >= block.promptRow + delta && bufferRow < block.endRow + delta) {
        return block;
      }
    }
    return null;
  }

  blockById(id: number): TerminalBlock | null {
    return this.completed.find((block) => block.id === id) ?? null;
  }

  reanchorOnResize(
    access: BlockRowAccess,
    scanRows: number = RESIZE_REANCHOR_SCAN_ROWS,
  ): 'ok' | 'all-stale' {
    const hadBlocks = this.completed.length > 0;
    if (!hadBlocks) return 'ok';
    const survivors: TerminalBlock[] = [];
    for (const block of this.completed) {
      const delta = reanchorDelta(block, access, scanRows);
      if (delta === null) continue;
      if (delta !== 0) {
        block.promptRow += delta;
        block.anchorRow += delta;
        if (block.outputStartRow !== undefined) block.outputStartRow += delta;
        if (block.endRow !== undefined) block.endRow += delta;
        if (block.inputStart) block.inputStart = { ...block.inputStart, row: block.inputStart.row + delta };
      }
      survivors.push(block);
    }
    this.completed = survivors;
    return survivors.length === 0 && hadBlocks ? 'all-stale' : 'ok';
  }

  clear(): void {
    this.completed = [];
    this.pending = null;
  }

  // The worker's rows are SCREEN-space rows of the VT dump, which equal live
  // buffer rows only because the dump was written into a fresh same-size terminal.
  seed(blocks: readonly SeededBlock[], rowTextAt?: (row: number) => string): void {
    this.completed = [];
    this.pending = null;
    let maxId = 0;
    for (const b of blocks) {
      if (b.id > maxId) maxId = b.id;
      const inputStart = b.inputRow !== undefined
        ? { row: b.inputRow, col: b.inputCol ?? 0 }
        : undefined;
      if (b.pending) {
        this.pending = {
          id: b.id,
          promptRow: b.promptRow,
          inputStart,
          outputStartRow: b.outputStartRow,
          command: b.command,
        };
        continue;
      }
      if (b.outputStartRow === undefined || b.endRow === undefined) continue;
      const anchorRow = b.inputRow ?? b.promptRow;
      this.completed.push({
        id: b.id,
        promptRow: b.promptRow,
        inputStart,
        outputStartRow: b.outputStartRow,
        endRow: b.endRow,
        command: b.command ?? '',
        exitCode: b.exitCode,
        anchorRow,
        anchorText: (rowTextAt?.(anchorRow) ?? '').slice(0, ANCHOR_LENGTH),
      });
    }
    this.nextId = Math.max(this.nextId, maxId + 1);
  }
}

export interface SeededBlock {
  id: number;
  pending: boolean;
  promptRow: number;
  inputRow?: number;
  inputCol?: number;
  outputStartRow?: number;
  endRow?: number;
  command?: string;
  exitCode?: number;
}

// startRow may be negative and endRow may exceed the last viewport row when the
// block extends past the visible area.
export interface BlockViewportSpan {
  startRow: number;
  endRow: number;
  visible: boolean;
  spansViewport: boolean;
}

export function blockViewportSpan(
  block: TerminalBlock,
  firstViewportBufferRow: number,
  viewportRows: number,
): BlockViewportSpan | null {
  if (block.endRow === undefined) return null;
  const startRow = block.promptRow - firstViewportBufferRow;
  const endRow = block.endRow - 1 - firstViewportBufferRow;
  return {
    startRow,
    endRow,
    visible: endRow >= 0 && startRow < viewportRows,
    spansViewport: startRow <= 0 && endRow >= viewportRows - 1,
  };
}

// A narrower pane clips rowText, and a tiny overlap (a 4-col pane) would match
// almost anything.
const MIN_ANCHOR_OVERLAP = 8;

function anchorMatches(anchorText: string, rowText: string): boolean {
  const overlap = Math.min(anchorText.length, rowText.length);
  if (overlap < Math.min(anchorText.length, MIN_ANCHOR_OVERLAP)) return false;
  return rowText.slice(0, overlap) === anchorText.slice(0, overlap);
}

// null means the content is gone (trimmed or rewritten) and extraction must not
// proceed.
export function reanchorDelta(
  block: TerminalBlock,
  access: BlockRowAccess,
  scanRows: number = REANCHOR_SCAN_ROWS,
): number | null {
  if (!block.anchorText) return 0;
  const total = access.totalRows();
  const matches = (row: number) => (
    row >= 0 && row < total && anchorMatches(block.anchorText, access.rowText(row))
  );
  if (matches(block.anchorRow)) return 0;
  for (let delta = 1; delta <= scanRows; delta += 1) {
    if (matches(block.anchorRow - delta)) return -delta;
    if (matches(block.anchorRow + delta)) return delta;
  }
  return null;
}

// firstViewportBufferRow must come from the SAME live scrollback `access` reads.
export function blockViewportSpanAnchored(
  block: TerminalBlock,
  access: BlockRowAccess,
  firstViewportBufferRow: number,
  viewportRows: number,
): BlockViewportSpan | null {
  if (block.endRow === undefined) return null;
  const delta = reanchorDelta(block, access);
  if (delta === null) return null;
  return blockViewportSpan(
    { ...block, promptRow: block.promptRow + delta, endRow: block.endRow + delta },
    firstViewportBufferRow,
    viewportRows,
  );
}

export interface ExtractedBlock {
  command: string;
  output: string;
}

export function extractBlock(block: TerminalBlock, access: BlockRowAccess): ExtractedBlock | null {
  if (block.outputStartRow === undefined || block.endRow === undefined) return null;
  const delta = reanchorDelta(block, access);
  if (delta === null) return null;
  const total = access.totalRows();
  const start = block.outputStartRow + delta;
  const end = Math.min(block.endRow + delta, total);
  const lines: string[] = [];
  for (let row = start; row < end; row += 1) {
    if (row < 0) continue;
    lines.push(access.rowText(row).replace(/\s+$/, ''));
  }
  while (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return { command: block.command, output: lines.join('\n') };
}
