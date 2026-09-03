import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { ALWAYS_ON_TOP_VAR } from './keyInputGuard.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const NATIVE_KEY_CALL = /(?:\.pressKey|\.pressKeyCode|\.pressEnter|\.typeText|\bpressShortcutKeys)\(/;
const OPT_OUT = new RegExp(`${ALWAYS_ON_TOP_VAR}\\s*(?:\\?\\?)?=\\s*'0'`);
const LAUNCH = /launchFreshAppAndConnect\(|client\.launchFreshApp\(|driver\.launchApp\(/;

const PROVEN_WITHOUT_OPT_OUT = {
  'macosDriver.mjs': 'the driver that posts the events; the guard it calls fails the press for the scenario',
  'linuxDriver.mjs': 'the Linux driver; Linux never makes the window non-focusable',
  'common.mjs': 'pressShortcutKeys presses on behalf of a scenario that carries the opt-out itself',
  'scenario-linux-shortcuts.mjs': 'returns before the launch off Linux, and Linux leaves the window focusable',
};

function harnessSources() {
  return fs.readdirSync(HARNESS_DIR)
    .filter((name) => name.endsWith('.mjs') && !name.endsWith('.test.mjs'))
    .sort();
}

function sweepNativeKeyInput() {
  return harnessSources().map((name) => {
    const source = fs.readFileSync(path.join(HARNESS_DIR, name), 'utf8');
    const keyCall = source.search(NATIVE_KEY_CALL);
    if (keyCall < 0) {
      return null;
    }
    const optOut = source.search(OPT_OUT);
    const launch = source.search(LAUNCH);
    return {
      name,
      optsOut: optOut >= 0,
      optsOutBeforeLaunch: optOut >= 0 && (launch < 0 || optOut < launch),
      proven: PROVEN_WITHOUT_OPT_OUT[name] ?? null,
    };
  }).filter(Boolean);
}

describe('always-on-top sweep', () => {
  const sweep = sweepNativeKeyInput();

  it('finds the scenarios that post native keys', () => {
    expect(sweep.length).toBeGreaterThan(10);
  });

  it.each(sweep)('$name either opts out of always-on-top or proves it need not', (entry) => {
    expect(
      entry.optsOut || entry.proven !== null,
      `${entry.name} posts native key events but never sets ${ALWAYS_ON_TOP_VAR}='0'. On macOS the harness `
      + 'launches the app always-on-top and non-focusable, so the keystroke lands nowhere. Set '
      + `process.env.${ALWAYS_ON_TOP_VAR} = '0' before the launch, or state here why this file does not need it.`,
    ).toBe(true);
  });

  it.each(sweep.filter((entry) => entry.optsOut))('$name opts out before it launches the app', (entry) => {
    expect(
      entry.optsOutBeforeLaunch,
      `${entry.name} sets ${ALWAYS_ON_TOP_VAR}='0' after the app launch, so the launched window is still `
      + 'non-focusable. Move the assignment above the launch.',
    ).toBe(true);
  });
});
