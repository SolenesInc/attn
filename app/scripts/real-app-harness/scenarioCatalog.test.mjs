import fs from 'node:fs';
import path from 'node:path';
import url from 'node:url';
import { describe, expect, it } from 'vitest';
import { TRIPWIRE_BINARIES } from './agentTripwire.mjs';
import {
  allowRealAgentsForRunner,
  resolveScenario,
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

  it('only allows agent binaries the tripwire knows how to shim', () => {
    for (const scenario of scenariosAllowingRealAgents()) {
      if (scenario.allowRealAgents === true) continue;
      expect(Array.isArray(scenario.allowRealAgents)).toBe(true);
      for (const binary of scenario.allowRealAgents) {
        expect(TRIPWIRE_BINARIES).toContain(binary);
      }
    }
  });

  it('refuses a runner outside the catalog rather than allowing every binary', () => {
    expect(() => allowRealAgentsForRunner('SOME-UNLISTED-PROBE'))
      .toThrow(/"SOME-UNLISTED-PROBE" has no scenarioCatalog\.mjs entry[\s\S]*allowRealAgents/);
    expect(() => allowRealAgentsForRunner(undefined)).toThrow(/no scenarioCatalog\.mjs entry/);
  });
});

describe('scenarioCatalog direct selection', () => {
  it('keeps hand-written soaks with unsafe teardown out of direct selection', () => {
    for (const id of ['offset-soak', 'perf-baseline', 'perf-cold-warm']) {
      expect(() => resolveScenario(id), id).toThrow(`Unknown scenario id: ${id}`);
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
