import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';
import { ALWAYS_ON_TOP_VAR } from './keyInputGuard.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const INPUT_METHODS = new Set([
  'pressKey', 'pressKeyCode', 'pressEnter', 'typeText',
  'clickWindow', 'rightClickWindow', 'dragWindow', 'movePointerInWindow',
]);
const INPUT_FUNCTIONS = new Set(['pressShortcutKeys']);
const FOREGROUND_METHODS = new Set(['activateApp']);
const DRIVER_FACTORIES = new Set(['createWindowDriver']);
const DRIVER_CLASSES = new Set(['MacOSDriver', 'LinuxDriver']);

// Files that post input through macOS on purpose; every other file hands the
// client to its driver so the app injects the events into its own window.
const DRIVES_THROUGH_MACOS = {
  'macosDriver.mjs': 'the driver itself; its CGEvent path serves the probes below',
  'linuxDriver.mjs': 'the Linux driver; xdotool under Xvfb has no foreground to steal',
  'common.mjs': 'pressShortcutKeys presses through whichever driver the scenario built',
  'scenario-focus-probe.mjs': 'measures which input paths steal focus, so it must use the one that does',
  'drive-wake-confirm.mjs': 'a hand-run driver for a native confirm dialog, which only macOS can reach',
};

// Files allowed to activate attn or launch it focusable, each with the reason.
const TAKES_THE_FOREGROUND = {
  'scenario-focus-probe.mjs': 'its subject is focus theft',
  'dev-launch-focus-probe.mjs': 'its subject is focus theft',
  'dev-raf-throttle-probe.mjs': 'its subject is the throttling a real focus state produces',
  'dev-wkwv-occlusion-probe.mjs': 'its subject is the occlusion a real window stack produces',
  'drive-wake-confirm.mjs': 'a native confirm dialog only answers a frontmost app',
  'scenario-delegation-chain.mjs': 'hover and :focus need an active page, which WebKit ties to a key window',
  'scenario-countdown-cancel.mjs': 'its pointer leg needs mouse moves, which WebKit delivers only to a key window',
};

function calledName(node, methods, functions) {
  if (ts.isPropertyAccessExpression(node.expression) && methods.has(node.expression.name.text)) {
    return node.expression.name.text;
  }
  return ts.isIdentifier(node.expression) && functions.has(node.expression.text) ? node.expression.text : null;
}

// `process.env.VAR = '0'`, or the `??=` form. Reading the parse and not the
// source text is what keeps a comment or a string saying so from counting.
function isOptOutAssignment(node) {
  if (!ts.isBinaryExpression(node)) return false;
  const assigns = node.operatorToken.kind === ts.SyntaxKind.EqualsToken
    || node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionEqualsToken;
  if (!assigns) return false;
  if (!ts.isStringLiteral(node.right) || node.right.text !== '0') return false;
  const target = node.left;
  if (!ts.isPropertyAccessExpression(target) || target.name.text !== ALWAYS_ON_TOP_VAR) return false;
  const env = target.expression;
  return ts.isPropertyAccessExpression(env)
    && env.name.text === 'env'
    && ts.isIdentifier(env.expression)
    && env.expression.text === 'process';
}

function passesClient(driverCall) {
  const [argument] = driverCall.arguments;
  if (!argument || !ts.isObjectLiteralExpression(argument)) return false;
  return argument.properties.some((property) => {
    if (ts.isShorthandPropertyAssignment(property)) return property.name.text === 'client';
    if (ts.isPropertyAssignment(property)) {
      return ts.isIdentifier(property.name) && property.name.text === 'client';
    }
    return false;
  });
}

export function analyzeFocus(source, fileName = 'scenario.mjs') {
  const tree = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const found = { inputCall: null, foregroundCall: null, driverCalls: 0, driversWithClient: 0, optsOut: false };
  const visit = (node) => {
    if (ts.isCallExpression(node)) {
      const input = calledName(node, INPUT_METHODS, INPUT_FUNCTIONS);
      if (input && found.inputCall === null) found.inputCall = input;
      const foreground = calledName(node, FOREGROUND_METHODS, new Set());
      if (foreground && found.foregroundCall === null) found.foregroundCall = foreground;
      if (calledName(node, new Set(), DRIVER_FACTORIES)) {
        found.driverCalls += 1;
        if (passesClient(node)) found.driversWithClient += 1;
      }
    } else if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && DRIVER_CLASSES.has(node.expression.text)) {
      found.driverCalls += 1;
      if (passesClient(node)) found.driversWithClient += 1;
    } else if (isOptOutAssignment(node)) {
      found.optsOut = true;
    }
    ts.forEachChild(node, visit);
  };
  visit(tree);
  return {
    drivesInput: found.inputCall !== null,
    inputCall: found.inputCall,
    injectsInApp: found.driverCalls > 0 && found.driversWithClient === found.driverCalls,
    takesForeground: found.foregroundCall !== null || found.optsOut,
    foregroundBy: found.foregroundCall ?? (found.optsOut ? `${ALWAYS_ON_TOP_VAR}='0'` : null),
  };
}

function harnessSources() {
  return fs.readdirSync(HARNESS_DIR)
    .filter((name) => name.endsWith('.mjs') && !name.endsWith('.test.mjs'))
    .sort();
}

function sweep() {
  return harnessSources().map((name) => ({
    name,
    ...analyzeFocus(fs.readFileSync(path.join(HARNESS_DIR, name), 'utf8'), name),
  }));
}

describe('analyzeFocus', () => {
  const press = "await driver.pressKey('a');";

  it('finds input through any of the entry points', () => {
    expect(analyzeFocus('await driver.typeText(x);').inputCall).toBe('typeText');
    expect(analyzeFocus('await driver.clickWindow(0.5, 0.5);').inputCall).toBe('clickWindow');
    expect(analyzeFocus('await pressShortcutKeys(client, driver, id);').inputCall).toBe('pressShortcutKeys');
    expect(analyzeFocus('// driver.pressKey(a) would be nice').drivesInput).toBe(false);
  });

  it('counts a driver as in-app only when every construction carries the client', () => {
    expect(analyzeFocus(`const d = createWindowDriver({ appPath, client });\n${press}`).injectsInApp).toBe(true);
    expect(analyzeFocus(`const d = createWindowDriver({ ...options, client });\n${press}`).injectsInApp).toBe(true);
    expect(analyzeFocus(`const d = createWindowDriver({ client: c });\n${press}`).injectsInApp).toBe(true);
    expect(analyzeFocus(`const d = createWindowDriver({ appPath });\n${press}`).injectsInApp).toBe(false);
    expect(analyzeFocus(`const d = createWindowDriver();\n${press}`).injectsInApp).toBe(false);
    expect(analyzeFocus(`const d = new MacOSDriver({ bundleId, client });\n${press}`).injectsInApp).toBe(true);
    expect(analyzeFocus(`const d = new MacOSDriver({ bundleId });\n${press}`).injectsInApp).toBe(false);
    expect(analyzeFocus('createWindowDriver({ client }); createWindowDriver({ appPath });').injectsInApp).toBe(false);
  });

  it('sees the foreground taken by activation or by opting out of always-on-top', () => {
    expect(analyzeFocus('await driver.activateApp();')).toMatchObject({ takesForeground: true, foregroundBy: 'activateApp' });
    expect(analyzeFocus(`process.env.${ALWAYS_ON_TOP_VAR} = '0';`).takesForeground).toBe(true);
    expect(analyzeFocus(`process.env.${ALWAYS_ON_TOP_VAR} ??= '0';`).takesForeground).toBe(true);
    expect(analyzeFocus(`process.env.${ALWAYS_ON_TOP_VAR} = '1';`).takesForeground).toBe(false);
    expect(analyzeFocus(`// process.env.${ALWAYS_ON_TOP_VAR} = '0'\n${press}`).takesForeground).toBe(false);
    expect(analyzeFocus(`const fix = "process.env.${ALWAYS_ON_TOP_VAR} = '0'";`).takesForeground).toBe(false);
  });
});

describe('focus-free sweep', () => {
  const entries = sweep();
  const driving = entries.filter((entry) => entry.drivesInput);

  it('finds the files that drive native input', () => {
    expect(driving.length).toBeGreaterThan(10);
  });

  it.each(driving)('$name injects input through the app or says why it posts through macOS', (entry) => {
    expect(
      entry.injectsInApp || entry.name in DRIVES_THROUGH_MACOS,
      `${entry.name} calls ${entry.inputCall} on a driver built without the automation client. Pass `
      + '`client` to createWindowDriver so the app injects the event into its own window, or state here '
      + 'why this file has to post through macOS.',
    ).toBe(true);
  });

  it.each(entries.filter((entry) => entry.takesForeground))('$name has a reason to take the foreground', (entry) => {
    expect(
      entry.name in TAKES_THE_FOREGROUND,
      `${entry.name} takes the foreground (${entry.foregroundBy}). Scenarios run beside the user's work: `
      + 'drop the activation or the opt-out, or state here why this file needs the real focus.',
    ).toBe(true);
  });

  it('keeps no stale exemption', () => {
    const drivingNames = new Set(driving.map((entry) => entry.name));
    const foregroundNames = new Set(entries.filter((entry) => entry.takesForeground).map((entry) => entry.name));
    const stale = [
      ...Object.keys(DRIVES_THROUGH_MACOS).filter((name) => !drivingNames.has(name)).map((name) => `DRIVES_THROUGH_MACOS: ${name}`),
      ...Object.keys(TAKES_THE_FOREGROUND).filter((name) => !foregroundNames.has(name)).map((name) => `TAKES_THE_FOREGROUND: ${name}`),
    ];
    expect(stale, `${stale.join(', ')} no longer need the exemption. Drop the entry.`).toEqual([]);
  });
});
