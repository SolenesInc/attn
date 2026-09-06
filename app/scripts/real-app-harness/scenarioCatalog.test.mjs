import fs from 'node:fs';
import path from 'node:path';
import url from 'node:url';
import { describe, expect, it } from 'vitest';
import { TRIPWIRE_BINARIES } from './agentTripwire.mjs';
import {
  allowRealAgentsForRunner,
  resolveScenario,
  resolveScenarios,
  scenarioCatalog,
  scenariosAllowingRealAgents,
} from './scenarioCatalog.mjs';

describe('scenarioCatalog agent tripwire flags', () => {
  it('names the runner id every entry arms the tripwire under', () => {
    for (const scenario of scenarioCatalog) {
      expect(scenario, `${scenario.id} must declare runnerId (null when it has no scenario runner)`)
        .toHaveProperty('runnerId');
    }
  });

  it('gives every platform skip a reason', () => {
    for (const scenario of scenarioCatalog) {
      for (const [platform, rule] of Object.entries(scenario.skipOn ?? {})) {
        const reason = typeof rule === 'string' ? rule : rule.reason;
        expect(reason, `${scenario.id} skipOn.${platform}`).toMatch(/\S/);
      }
    }
  });

  it('only allows agent binaries the tripwire knows how to shim', () => {
    for (const scenario of scenariosAllowingRealAgents()) {
      if (scenario.allowRealAgents === true) continue;
      expect(Array.isArray(scenario.allowRealAgents)).toBe(true);
      for (const binary of scenario.allowRealAgents) {
        expect(TRIPWIRE_BINARIES).toContain(binary);
      }
    }
  });

  it('keeps the model-free scenarios armed', () => {
    expect(allowRealAgentsForRunner('NUDGE-TRIGGER')).toBeUndefined();
    expect(allowRealAgentsForRunner('TERMINAL-ANNOTATIONS')).toBeUndefined();
  });

  it('lets the pi scenarios run pi and nothing else', () => {
    // pi is a real binary this scenario execs against a stub model, so only pi
    // is allowed and claude/codex/copilot stay armed.
    expect(allowRealAgentsForRunner('PI-AUTOMODE')).toEqual(['pi']);
  });

  it('keeps the resume family armed on the mock agent', () => {
    for (const runnerId of ['CRASH-REC', 'RECOVERABLE-AUTO-REVIVE', 'TR-CODEX-RESUME']) {
      expect(allowRealAgentsForRunner(runnerId), runnerId).toBeUndefined();
    }
  });

  it('refuses a runner outside the catalog rather than allowing every binary', () => {
    expect(() => allowRealAgentsForRunner('SOME-UNLISTED-PROBE'))
      .toThrow(/"SOME-UNLISTED-PROBE" has no scenarioCatalog\.mjs entry[\s\S]*allowRealAgents/);
    expect(() => allowRealAgentsForRunner(undefined)).toThrow(/no scenarioCatalog\.mjs entry/);
  });

  it('keeps the turn-accounting family armed on the mock agent', () => {
    for (const runnerId of ['TR-201', 'TR-204', 'TR-301', 'TR-401', 'TR-401-CODEX-MAIN', 'TR-402']) {
      expect(allowRealAgentsForRunner(runnerId), runnerId).toBeUndefined();
    }
  });

  it('keeps the scenarios that never prompt their agent armed on the mock agent', () => {
    for (const runnerId of [
      'FOCUS-PROBE',
      'GHOSTTY-SCROLLBACK-ANCHOR',
      'SNAPSHOT-SCROLLBACK-RESTORE',
      'DELEGATE-WORKSPACE-PLACEMENT',
    ]) {
      expect(allowRealAgentsForRunner(runnerId), runnerId).toBeUndefined();
    }
  });

  it('keeps the queue family armed on the mock agent', () => {
    for (const runnerId of ['AGENT-QUEUE', 'COUNTDOWN-CANCEL', 'SETTLE-TYPING-HOLD']) {
      expect(allowRealAgentsForRunner(runnerId), runnerId).toBeUndefined();
    }
  });

  it('takes the permissive flag when one runner id serves several entries', () => {
    const catalog = [
      { id: 'a', runnerId: 'DUO' },
      { id: 'b', runnerId: 'DUO', allowRealAgents: true },
    ];

    expect(allowRealAgentsForRunner('DUO', catalog)).toBe(true);
    expect(allowRealAgentsForRunner('DUO', [
      { id: 'a', runnerId: 'DUO', allowRealAgents: ['pi'] },
      { id: 'b', runnerId: 'DUO', allowRealAgents: ['pi', 'codex'] },
    ])).toEqual(['pi', 'codex']);
  });
});

describe('scenarioCatalog soakOnly handling', () => {
  it('has the focus-probe entry marked soakOnly', () => {
    const focusProbe = scenarioCatalog.find((scenario) => scenario.id === 'focus-probe');

    expect(focusProbe).toBeDefined();
    expect(focusProbe.soakOnly).toBe(true);
  });

  it('excludes soakOnly entries from the full matrix sweep', () => {
    const scenarios = resolveScenarios([]);

    expect(scenarios.some((scenario) => scenario.soakOnly)).toBe(false);
    expect(scenarios.some((scenario) => scenario.id === 'focus-probe')).toBe(false);
    // The rest of the catalog is untouched.
    expect(scenarios.length).toBe(scenarioCatalog.filter((scenario) => !scenario.soakOnly).length);
  });

  it('rejects explicit matrix selection of a soakOnly scenario', () => {
    expect(() => resolveScenarios(['focus-probe'])).toThrow('Unknown scenario id: focus-probe');
  });

  it('still resolves regular scenarios by explicit matrix selection', () => {
    const scenarios = resolveScenarios(['ghostty-scroll']);

    expect(scenarios).toHaveLength(1);
    expect(scenarios[0].id).toBe('ghostty-scroll');
  });

  it('resolves a soakOnly scenario via direct single-scenario resolution', () => {
    const scenario = resolveScenario('focus-probe');

    expect(scenario.id).toBe('focus-probe');
    expect(scenario.command).toEqual(['pnpm', 'run', 'real-app:focus-probe']);
  });

  it('throws on an unknown id in direct single-scenario resolution', () => {
    expect(() => resolveScenario('does-not-exist')).toThrow('Unknown scenario id: does-not-exist');
  });
});

describe('scenarioCatalog daemon isolation', () => {
  it('stops the daemon after scenarios whose remote routing changes per run', () => {
    for (const id of ['tr205-probe-codex', 'tr205-probe-claude', 'tr502', 'tr504']) {
      expect(resolveScenario(id).freshWorldAfter, id).toBe(true);
    }
  });
});

// A hand-rolled main() that never builds a runner arms no tripwire at all and
// is out of this net; every createScenarioRunner caller is in it.
describe('every scenario built on the scenario runner', () => {
  const harnessDir = path.dirname(url.fileURLToPath(import.meta.url));
  const runnerIds = new Set(scenarioCatalog.map((scenario) => scenario.runnerId).filter(Boolean));

  it('declares its tripwire in the catalog or on the runner', () => {
    const undeclared = [];
    for (const file of fs.readdirSync(harnessDir).filter((name) => name.startsWith('scenario-') && name.endsWith('.mjs'))) {
      const source = fs.readFileSync(path.join(harnessDir, file), 'utf8');
      if (!source.includes('createScenarioRunner(')) {
        continue;
      }
      const lines = source.split('\n');
      lines.forEach((line, index) => {
        const match = /scenarioId: '([^']+)'/.exec(line);
        if (!match || runnerIds.has(match[1])) {
          return;
        }
        if (!lines.slice(index, index + 12).some((option) => option.includes('allowRealAgents:'))) {
          undeclared.push(`${file}: ${match[1]}`);
        }
      });
    }

    expect(undeclared, 'add a scenarioCatalog entry or pass allowRealAgents to createScenarioRunner').toEqual([]);
  });
});
