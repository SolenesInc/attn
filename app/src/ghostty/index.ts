import type { GhosttyExports } from './abi';
import { TERMINAL_INPUT_KEY_NAMES } from './input';
import { assertGhosttyKeyNames, readGhosttyKeyAbi, type GhosttyKeyAbi } from './keyEncoder';
import { GhosttyTerminal, type GhosttyTerminalConfig } from './terminal';

export { CellFlags, GhosttyTerminal } from './terminal';
export { attachTerminalInput } from './input';
export type { TerminalInputOptions, TerminalInputTarget } from './input';
export type {
  GhosttyCell,
  GhosttyTerminalConfig,
  RGB,
  RenderStateColors,
  RenderStateCursor,
  SnapshotHistoryDecoder,
} from './terminal';
export type { TerminalKeyAction, TerminalKeyEvent } from './keyEncoder';
export { DIRTY_FALSE, DIRTY_FULL, DIRTY_PARTIAL } from './abi';

export class Ghostty {
  readonly exports: GhosttyExports;
  private readonly keyAbi: GhosttyKeyAbi;

  constructor(instance: WebAssembly.Instance) {
    this.exports = instance.exports as GhosttyExports;
    this.keyAbi = readGhosttyKeyAbi(this.exports);
    assertGhosttyKeyNames(this.keyAbi, TERMINAL_INPUT_KEY_NAMES);
  }

  createTerminal(cols = 80, rows = 24, config: GhosttyTerminalConfig = {}): GhosttyTerminal {
    return new GhosttyTerminal(this.exports, this.keyAbi, cols, rows, config);
  }
}
