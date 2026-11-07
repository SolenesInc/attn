import { asciiBytes, indexOfBytes, partialPatternSuffixLength, withPendingBytes } from './terminalByteScan';

const SYNC_OUTPUT_MARKER = asciiBytes('\x1b[?2026');
const [SYNC_OUTPUT_START] = asciiBytes('h');
const [SYNC_OUTPUT_END] = asciiBytes('l');
const ESC = 0x1b;

export interface SynchronizedOutputState {
  active: boolean;
  pending: Uint8Array | null;
}

export interface SynchronizedOutputParseResult {
  state: SynchronizedOutputState;
  shouldRender: boolean;
}

export function emptySynchronizedOutputState(): SynchronizedOutputState {
  return { active: false, pending: null };
}

export function parseSynchronizedOutput(
  state: SynchronizedOutputState,
  chunk: Uint8Array,
): SynchronizedOutputParseResult {
  if (!state.pending && chunk.indexOf(ESC) === -1) {
    return { state, shouldRender: !state.active };
  }

  const buffer = withPendingBytes(state.pending, chunk);
  let active = state.active;
  let position = 0;
  for (;;) {
    const marker = indexOfBytes(buffer, SYNC_OUTPUT_MARKER, position);
    if (marker < 0 || marker + SYNC_OUTPUT_MARKER.length >= buffer.length) break;
    const final = buffer[marker + SYNC_OUTPUT_MARKER.length];
    if (final === SYNC_OUTPUT_START) active = true;
    if (final === SYNC_OUTPUT_END) active = false;
    position = marker + SYNC_OUTPUT_MARKER.length;
  }

  const hold = partialOrWholeMarkerSuffixLength(buffer);
  return {
    state: { active, pending: hold > 0 ? buffer.slice(buffer.length - hold) : null },
    shouldRender: !active,
  };
}

function partialOrWholeMarkerSuffixLength(buffer: Uint8Array): number {
  if (indexOfBytes(buffer, SYNC_OUTPUT_MARKER, buffer.length - SYNC_OUTPUT_MARKER.length) >= 0) {
    return SYNC_OUTPUT_MARKER.length;
  }
  return partialPatternSuffixLength(buffer, SYNC_OUTPUT_MARKER);
}
