import { describe, expect, it } from 'vitest';
import { bumpFsChangeSignal, fsChangeSignalKey } from './fsChangeSignals';

// App.tsx has no render-level suite for this wiring, so the extracted
// key/reducer functions are exercised directly.

const NOTEBOOK_ROOT = '/Users/victor/attn-notebook';
const ROOT_A = '/Users/victor/code/repo-a';
const ROOT_B = '/Users/victor/code/repo-b';

describe('fsChangeSignalKey', () => {
  it('uses the event root verbatim when present', () => {
    expect(fsChangeSignalKey(ROOT_A, NOTEBOOK_ROOT)).toBe(ROOT_A);
  });

  it('falls back to the effective notebook root for an empty/missing root', () => {
    expect(fsChangeSignalKey('', NOTEBOOK_ROOT)).toBe(NOTEBOOK_ROOT);
  });
});

describe('bumpFsChangeSignal (per-root fs_changed routing)', () => {
  it('bumps only the signal for the event root, leaving other roots untouched', () => {
    let signals: Record<string, number> = { [ROOT_A]: 1, [ROOT_B]: 5 };
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    expect(signals).toEqual({ [ROOT_A]: 2, [ROOT_B]: 5 });
  });

  it('an event for root A never bumps a tile keyed to root B', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    expect(signals[ROOT_B]).toBeUndefined();
    expect(signals[ROOT_A]).toBe(1);
  });

  it('an empty-root event reaches notebook-rooted tiles (keyed to the effective notebook root)', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, '', NOTEBOOK_ROOT);
    expect(signals[NOTEBOOK_ROOT]).toBe(1);
    // It must not create a spurious '' key that no tile would ever look up.
    expect(signals['']).toBeUndefined();
  });

  it('a notebook-root event and an empty-root event land on the same key (both mean notebook-rooted)', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, NOTEBOOK_ROOT, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, '', NOTEBOOK_ROOT);
    expect(signals[NOTEBOOK_ROOT]).toBe(2);
  });

  it('accumulates independently across many roots', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, ROOT_B, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, '', NOTEBOOK_ROOT);
    expect(signals).toEqual({ [ROOT_A]: 2, [ROOT_B]: 1, [NOTEBOOK_ROOT]: 1 });
  });
});
