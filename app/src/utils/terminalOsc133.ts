import { asciiBytes, indexOfBytes, partialPatternSuffixLength, withPendingBytes } from './terminalByteScan';

// The parser works on raw bytes: decoding to a string and re-encoding would corrupt
// multibyte characters split across PTY chunks.

export type Osc133Marker =
  | { kind: 'prompt-start' }
  | { kind: 'input-start' }
  | { kind: 'pre-exec'; cmdline?: string }
  | { kind: 'command-end'; exitCode?: number };

export interface Osc133State {
  pending: Uint8Array | null;
}

export interface Osc133Segment {
  bytes: Uint8Array;
  marker?: Osc133Marker;
}

export interface Osc133ParseResult {
  state: Osc133State;
  segments: Osc133Segment[];
}

const PREFIX = asciiBytes('\x1b]133;');
const BEL = 0x07;
const ESC = 0x1b;
const BACKSLASH = 0x5c;
const MAX_PENDING_BYTES = 4096;

const payloadDecoder = new TextDecoder();

function markerFromPayload(payload: string): Osc133Marker | undefined {
  switch (payload[0]) {
    case 'A':
      return { kind: 'prompt-start' };
    case 'B':
      return { kind: 'input-start' };
    case 'C': {
      let cmdline: string | undefined;
      for (const part of payload.slice(2).split(';')) {
        if (part.startsWith('cmdline_url=')) {
          try {
            cmdline = decodeURIComponent(part.slice('cmdline_url='.length));
          } catch {
            cmdline = undefined;
          }
        } else if (part.startsWith('cmdline=') && cmdline === undefined) {
          cmdline = part.slice('cmdline='.length);
        }
      }
      return { kind: 'pre-exec', cmdline };
    }
    case 'D': {
      const exitCode = Number.parseInt(payload.slice(2), 10);
      return { kind: 'command-end', exitCode: Number.isFinite(exitCode) ? exitCode : undefined };
    }
    default:
      return undefined;
  }
}

export function emptyOsc133State(): Osc133State {
  return { pending: null };
}

export function parseOsc133(state: Osc133State, chunk: Uint8Array): Osc133ParseResult {
  if (!state.pending && chunk.indexOf(ESC) === -1) {
    return { state, segments: chunk.length > 0 ? [{ bytes: chunk }] : [] };
  }

  const buffer = withPendingBytes(state.pending, chunk);

  const segments: Osc133Segment[] = [];
  let segmentStart = 0;
  let searchFrom = 0;

  for (;;) {
    const markerStart = indexOfBytes(buffer, PREFIX, searchFrom);
    if (markerStart === -1) {
      const hold = partialPatternSuffixLength(buffer, PREFIX);
      const flushEnd = buffer.length - hold;
      if (flushEnd > segmentStart) {
        segments.push({ bytes: buffer.subarray(segmentStart, flushEnd) });
      }
      return {
        state: { pending: hold > 0 ? buffer.slice(flushEnd) : null },
        segments,
      };
    }

    let terminatorEnd = -1;
    for (let i = markerStart + PREFIX.length; i < buffer.length; i += 1) {
      if (buffer[i] === BEL) {
        terminatorEnd = i + 1;
        break;
      }
      if (buffer[i] === ESC && i + 1 < buffer.length && buffer[i + 1] === BACKSLASH) {
        terminatorEnd = i + 2;
        break;
      }
    }
    if (terminatorEnd === -1) {
      if (buffer.length - markerStart > MAX_PENDING_BYTES) {
        segments.push({ bytes: buffer.subarray(segmentStart) });
        return { state: { pending: null }, segments };
      }
      if (markerStart > segmentStart) {
        segments.push({ bytes: buffer.subarray(segmentStart, markerStart) });
      }
      return { state: { pending: buffer.slice(markerStart) }, segments };
    }

    const payloadEnd = buffer[terminatorEnd - 1] === BEL ? terminatorEnd - 1 : terminatorEnd - 2;
    const payload = payloadDecoder.decode(buffer.subarray(markerStart + PREFIX.length, payloadEnd));
    segments.push({
      bytes: buffer.subarray(segmentStart, terminatorEnd),
      marker: markerFromPayload(payload),
    });
    segmentStart = terminatorEnd;
    searchFrom = terminatorEnd;
  }
}
