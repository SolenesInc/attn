import { describe, expect, it } from 'vitest';
import { emptySynchronizedOutputState, parseSynchronizedOutput, type SynchronizedOutputState } from './terminalSynchronizedOutput';

const encoder = new TextEncoder();

describe('parseSynchronizedOutput', () => {
  it('renders ordinary output immediately', () => {
    const parsed = parseSynchronizedOutput(emptySynchronizedOutputState(), encoder.encode('hello'));

    expect(parsed.shouldRender).toBe(true);
    expect(parsed.state).toEqual(emptySynchronizedOutputState());
  });

  it('suppresses intermediate synchronized output and renders on end', () => {
    let state: SynchronizedOutputState = emptySynchronizedOutputState();
    let parsed = parseSynchronizedOutput(state, encoder.encode('before\x1b[?2026hredraw '));
    expect(parsed.shouldRender).toBe(false);
    expect(parsed.state.active).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, encoder.encode('more redraw'));
    expect(parsed.shouldRender).toBe(false);
    expect(parsed.state.active).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, encoder.encode('done\x1b[?2026lafter'));
    expect(parsed.shouldRender).toBe(true);
    expect(parsed.state.active).toBe(false);
  });

  it('handles split synchronized-output markers across chunks', () => {
    let state: SynchronizedOutputState = emptySynchronizedOutputState();
    let parsed = parseSynchronizedOutput(state, encoder.encode('\x1b[?20'));
    expect(parsed.shouldRender).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, encoder.encode('26hredraw'));
    expect(parsed.shouldRender).toBe(false);
    expect(parsed.state.active).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, encoder.encode('\x1b[?202'));
    expect(parsed.shouldRender).toBe(false);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, encoder.encode('6l'));
    expect(parsed.shouldRender).toBe(true);
    expect(parsed.state.active).toBe(false);
  });

  it('ends in the same state wherever the chunks split', () => {
    const output = encoder.encode('é\x1b[31m\x1b[?2026hredraw\x1b[?2026lü\x1b[?2026h\x1b[?2025l\x1b');
    for (let split = 0; split <= output.length; split += 1) {
      const first = parseSynchronizedOutput(emptySynchronizedOutputState(), output.subarray(0, split));
      const second = parseSynchronizedOutput(first.state, output.subarray(split));
      expect(second.shouldRender, `split at ${split}`).toBe(false);
      expect(second.state.active, `split at ${split}`).toBe(true);
    }
  });
});
