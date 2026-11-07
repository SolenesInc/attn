import { describe, expect, it } from 'vitest';
import { emptyOsc52State, parseOsc52Writes } from './terminalOsc';

const encoder = new TextEncoder();

function parseChunks(chunks: Uint8Array[]) {
  let state = emptyOsc52State();
  const payloads: string[] = [];
  for (const chunk of chunks) {
    const parsed = parseOsc52Writes(state, chunk);
    state = parsed.state;
    payloads.push(...parsed.payloads);
  }
  return { state, payloads };
}

describe('parseOsc52Writes', () => {
  it('parses clipboard writes split across PTY chunks', () => {
    const { state, payloads } = parseChunks(['prefix\x1b]5', '2;c;aGVs', 'bG8=\x07tail'].map((chunk) => encoder.encode(chunk)));
    expect(payloads).toEqual(['aGVsbG8=']);
    expect(state.pending).toBeNull();
  });

  it('accepts ST termination and rejects clipboard read queries', () => {
    const { payloads } = parseChunks([encoder.encode('\x1b]52;c;?\x1b\\\x1b]52;c;d29ybGQ=\x1b\\')]);
    expect(payloads).toEqual(['d29ybGQ=']);
  });

  it('finds every write wherever the chunks split, among other escapes and multibyte text', () => {
    const output = encoder.encode('é\x1b[31m\x1b]52;c;YQ==\x07\x1b]0;title\x07ü\x1b]52;c;Yg==\x1b\\\x1b[0m\x1b]52;c;?\x07\x1b');
    for (let split = 0; split <= output.length; split += 1) {
      const { payloads } = parseChunks([output.subarray(0, split), output.subarray(split)]);
      expect(payloads, `split at ${split}`).toEqual(['YQ==', 'Yg==']);
    }
  });
});
