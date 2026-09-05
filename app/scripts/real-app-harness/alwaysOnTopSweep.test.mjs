import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';
import { ALWAYS_ON_TOP_VAR } from './keyInputGuard.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const KEY_METHODS = new Set(['pressKey', 'pressKeyCode', 'pressEnter', 'typeText']);
const KEY_FUNCTIONS = new Set(['pressShortcutKeys']);
const LAUNCH_METHODS = new Set(['launchApp', 'launchFreshApp']);
const LAUNCH_FUNCTIONS = new Set(['launchFreshAppAndConnect']);

const PROVEN_WITHOUT_OPT_OUT = {
  'macosDriver.mjs': 'the driver that posts the events; the guard it calls fails the press for the scenario',
  'linuxDriver.mjs': 'the Linux driver; Linux never makes the window non-focusable',
  'common.mjs': 'pressShortcutKeys presses on behalf of a scenario that carries the opt-out itself',
  'scenario-linux-shortcuts.mjs': 'returns before the launch off Linux, and Linux leaves the window focusable',
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

function analyzeAlwaysOnTop(source, fileName = 'scenario.mjs') {
  const tree = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const found = { keyCall: null, optOut: -1, launch: -1 };
  const visit = (node) => {
    if (ts.isCallExpression(node)) {
      const key = calledName(node, KEY_METHODS, KEY_FUNCTIONS);
      if (key && found.keyCall === null) found.keyCall = key;
      const launch = calledName(node, LAUNCH_METHODS, LAUNCH_FUNCTIONS);
      if (launch && found.launch < 0) found.launch = node.getStart(tree);
    } else if (isOptOutAssignment(node) && found.optOut < 0) {
      found.optOut = node.getStart(tree);
    }
    ts.forEachChild(node, visit);
  };
  visit(tree);
  return {
    pressesKeys: found.keyCall !== null,
    keyCall: found.keyCall,
    optsOut: found.optOut >= 0,
    optsOutBeforeLaunch: found.optOut >= 0 && (found.launch < 0 || found.optOut < found.launch),
  };
}

function harnessSources() {
  return fs.readdirSync(HARNESS_DIR)
    .filter((name) => name.endsWith('.mjs') && !name.endsWith('.test.mjs'))
    .sort();
}

function sweepNativeKeyInput() {
  return harnessSources().map((name) => {
    const analysis = analyzeAlwaysOnTop(fs.readFileSync(path.join(HARNESS_DIR, name), 'utf8'), name);
    if (!analysis.pressesKeys) {
      return null;
    }
    return { name, ...analysis, proven: PROVEN_WITHOUT_OPT_OUT[name] ?? null };
  }).filter(Boolean);
}

describe('analyzeAlwaysOnTop', () => {
  const press = "await driver.pressKey('a');";

  it('counts a real assignment, in either form', () => {
    expect(analyzeAlwaysOnTop(`process.env.${ALWAYS_ON_TOP_VAR} = '0';\n${press}`).optsOut).toBe(true);
    expect(analyzeAlwaysOnTop(`process.env.${ALWAYS_ON_TOP_VAR} ??= '0';\n${press}`).optsOut).toBe(true);
  });

  it('does not count a comment that says the assignment', () => {
    const source = `// process.env.${ALWAYS_ON_TOP_VAR} = '0' before the launch\n${press}`;
    expect(analyzeAlwaysOnTop(source)).toMatchObject({ pressesKeys: true, optsOut: false });
  });

  it('does not count a string literal that says the assignment', () => {
    const source = `const fix = "process.env.${ALWAYS_ON_TOP_VAR} = '0'";\n${press}`;
    expect(analyzeAlwaysOnTop(source)).toMatchObject({ pressesKeys: true, optsOut: false });
  });

  it('does not count opting back in', () => {
    expect(analyzeAlwaysOnTop(`process.env.${ALWAYS_ON_TOP_VAR} = '1';\n${press}`).optsOut).toBe(false);
  });

  it('finds a press through any of the entry points', () => {
    expect(analyzeAlwaysOnTop('await driver.typeText(x);').keyCall).toBe('typeText');
    expect(analyzeAlwaysOnTop('await driver.pressEnter();').keyCall).toBe('pressEnter');
    expect(analyzeAlwaysOnTop('await pressShortcutKeys(client, driver, id);').keyCall).toBe('pressShortcutKeys');
    expect(analyzeAlwaysOnTop('// driver.pressKey(a) would be nice').pressesKeys).toBe(false);
  });

  it('reads the order of the assignment against the launch', () => {
    const before = `process.env.${ALWAYS_ON_TOP_VAR} = '0';\nawait launchFreshAppAndConnect(c, o);\n${press}`;
    const after = `await launchFreshAppAndConnect(c, o);\nprocess.env.${ALWAYS_ON_TOP_VAR} = '0';\n${press}`;
    expect(analyzeAlwaysOnTop(before).optsOutBeforeLaunch).toBe(true);
    expect(analyzeAlwaysOnTop(after).optsOutBeforeLaunch).toBe(false);
  });
});

describe('always-on-top sweep', () => {
  const sweep = sweepNativeKeyInput();

  it('finds the scenarios that post native keys', () => {
    expect(sweep.length).toBeGreaterThan(10);
  });

  it.each(sweep)('$name either opts out of always-on-top or proves it need not', (entry) => {
    expect(
      entry.optsOut || entry.proven !== null,
      `${entry.name} calls ${entry.keyCall} but never sets ${ALWAYS_ON_TOP_VAR}='0'. On macOS the harness `
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

  it('keeps no stale exemption', () => {
    const swept = new Set(sweep.map((entry) => entry.name));
    const stale = Object.keys(PROVEN_WITHOUT_OPT_OUT).filter((name) => !swept.has(name));
    expect(
      stale,
      `PROVEN_WITHOUT_OPT_OUT excuses ${stale.join(', ')}, which no longer posts native keys. Drop the entry.`,
    ).toEqual([]);
  });
});
