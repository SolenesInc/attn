import fs from 'node:fs';
import path from 'node:path';
import url from 'node:url';
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { TRIPWIRE_BINARIES } from './agentTripwire.mjs';
import {
  allowRealAgentsForRunner,
  resolveScenario,
  resolveScenarios,
  scenarioCatalog,
  scenariosAllowingRealAgents,
} from './scenarioCatalog.mjs';
import { planShards, readRecordedDurations } from './shardPlan.mjs';

describe('scenarioCatalog agent tripwire flags', () => {
  it('names the runner id every entry arms the tripwire under', () => {
    for (const scenario of scenarioCatalog) {
      assert.ok('runnerId' in scenario, `${scenario.id} must declare runnerId (null when it has no scenario runner)`);
    }
  });

  it('only allows agent binaries the tripwire knows how to shim', () => {
    for (const scenario of scenariosAllowingRealAgents()) {
      if (scenario.allowRealAgents === true) continue;
      assert.ok(Array.isArray(scenario.allowRealAgents));
      for (const binary of scenario.allowRealAgents) {
        assert.ok(TRIPWIRE_BINARIES.includes(binary));
      }
    }
  });

  it('refuses a runner outside the catalog rather than allowing every binary', () => {
    assert.throws(
      () => allowRealAgentsForRunner('SOME-UNLISTED-PROBE'),
      /"SOME-UNLISTED-PROBE" has no scenarioCatalog\.mjs entry[\s\S]*allowRealAgents/,
    );
    assert.throws(() => allowRealAgentsForRunner(undefined), /no scenarioCatalog\.mjs entry/);
  });
});

describe('the Linux serial matrix', () => {
  it('has a recorded duration for each Linux scenario, so the App acceptance shards can be planned', () => {
    assert.doesNotThrow(() => planShards(resolveScenarios([]), readRecordedDurations(), 2, 'linux', {}));
  });
});

describe('scenarioCatalog direct selection', () => {
  it('keeps hand-written perf soaks out of direct selection', () => {
    for (const id of ['perf-baseline', 'perf-cold-warm']) {
      assert.throws(() => resolveScenario(id), new RegExp(`Unknown scenario id: ${id}`));
    }
  });
});

describe('every catalog command', () => {
  const harnessDir = path.dirname(url.fileURLToPath(import.meta.url));
  const appDir = path.resolve(harnessDir, '../..');

  it('runs a scenario file that exists', () => {
    const broken = scenarioCatalog
      .filter(({ command }) => command[0] !== 'node' || !/^scripts\/real-app-harness\/[\w.-]+\.mjs$/.test(command[1] ?? '') || !fs.existsSync(path.join(appDir, command[1])))
      .map(({ id, command }) => `${id}: ${command.join(' ')}`);

    assert.deepEqual(broken, []);
  });

  it('runs under the runner id the catalog names', () => {
    const mismatched = [];
    for (const scenario of scenarioCatalog.filter((entry) => entry.runnerId)) {
      const file = scenario.command[1];
      const declared = /scenarioId: '([^']+)'/.exec(fs.readFileSync(path.join(appDir, file), 'utf8'))?.[1];
      if (declared && declared !== scenario.runnerId) {
        mismatched.push(`${scenario.id}: catalog ${scenario.runnerId}, ${file} ${declared}`);
      }
    }

    assert.deepEqual(mismatched, [], 'receipts and the real-agent policy follow the runner id');
  });
});

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

    assert.deepEqual(undeclared, [], 'add a scenarioCatalog entry or pass allowRealAgents to createScenarioRunner');
  });
});
