const textEncoder = new TextEncoder();

export function asciiBytes(text: string): Uint8Array {
  return textEncoder.encode(text);
}

export function indexOfBytes(buffer: Uint8Array, pattern: Uint8Array, from: number): number {
  const last = buffer.length - pattern.length;
  for (let i = buffer.indexOf(pattern[0], from); i !== -1 && i <= last; i = buffer.indexOf(pattern[0], i + 1)) {
    let matched = true;
    for (let j = 1; j < pattern.length; j += 1) {
      if (buffer[i + j] !== pattern[j]) {
        matched = false;
        break;
      }
    }
    if (matched) return i;
  }
  return -1;
}

export function partialPatternSuffixLength(buffer: Uint8Array, pattern: Uint8Array): number {
  const max = Math.min(buffer.length, pattern.length - 1);
  for (let length = max; length > 0; length -= 1) {
    let matched = true;
    for (let i = 0; i < length; i += 1) {
      if (buffer[buffer.length - length + i] !== pattern[i]) {
        matched = false;
        break;
      }
    }
    if (matched) return length;
  }
  return 0;
}

export function withPendingBytes(pending: Uint8Array | null, chunk: Uint8Array): Uint8Array {
  if (!pending || pending.length === 0) return chunk;
  const buffer = new Uint8Array(pending.length + chunk.length);
  buffer.set(pending, 0);
  buffer.set(chunk, pending.length);
  return buffer;
}
