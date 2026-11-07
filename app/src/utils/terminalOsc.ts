import { asciiBytes, indexOfBytes, partialPatternSuffixLength, withPendingBytes } from './terminalByteScan';

const OSC_52_PREFIX = asciiBytes('\x1b]52;');
const ESC = 0x1b;
const BEL = 0x07;
const BACKSLASH = 0x5c;
const SEMICOLON = 0x3b;
const QUERY = 0x3f;
const MAX_PENDING_OSC_BYTES = 16 * 1024 * 1024;

const payloadDecoder = new TextDecoder();

export interface Osc52State {
  pending: Uint8Array | null;
}

export interface Osc52ParseResult {
  state: Osc52State;
  payloads: string[];
}

export function emptyOsc52State(): Osc52State {
  return { pending: null };
}

export function parseOsc52Writes(state: Osc52State, chunk: Uint8Array): Osc52ParseResult {
  if (!state.pending && chunk.indexOf(ESC) === -1) {
    return { state, payloads: [] };
  }

  const buffer = withPendingBytes(state.pending, chunk);
  const payloads: string[] = [];
  let position = 0;

  while (position < buffer.length) {
    const start = indexOfBytes(buffer, OSC_52_PREFIX, position);
    if (start < 0) {
      const hold = partialPatternSuffixLength(buffer.subarray(position), OSC_52_PREFIX);
      return { state: { pending: hold > 0 ? buffer.slice(buffer.length - hold) : null }, payloads };
    }

    const commandStart = start + OSC_52_PREFIX.length;
    let commandEnd = -1;
    let terminatorLength = 1;
    for (let i = commandStart; i < buffer.length; i += 1) {
      if (buffer[i] === BEL) {
        commandEnd = i;
        break;
      }
      if (buffer[i] === ESC && buffer[i + 1] === BACKSLASH) {
        commandEnd = i;
        terminatorLength = 2;
        break;
      }
    }
    if (commandEnd < 0) {
      return { state: { pending: buffer.slice(start, start + MAX_PENDING_OSC_BYTES) }, payloads };
    }

    const separator = buffer.subarray(commandStart, commandEnd).indexOf(SEMICOLON);
    if (separator >= 0) {
      const payload = buffer.subarray(commandStart + separator + 1, commandEnd);
      if (payload.length !== 1 || payload[0] !== QUERY) payloads.push(payloadDecoder.decode(payload));
    }
    position = commandEnd + terminatorLength;
  }

  return { state: { pending: null }, payloads };
}
