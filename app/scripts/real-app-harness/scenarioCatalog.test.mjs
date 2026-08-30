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
    // pi is a real binary these scenarios exec against a stub model or a
    // recording, so only pi is allowed and claude/codex/copilot stay armed.
    expect(allowRealAgentsForRunner('PI-AUTOMODE')).toEqual(['pi']);
    expect(allowRealAgentsForRunner('NISSE-MARKDOWN-STREAM')).toEqual(['pi']);
  });

  it('allows every binary for a scenario that still launches a real agent', () => {
    expect(allowRealAgentsForRunner('AGENT-QUEUE')).toBe(true);
  });

  it('allows every binary for a runner outside the catalog', () => {
    expect(allowRealAgentsForRunner('SOME-UNLISTED-PROBE')).toBe(true);
    expect(allowRealAgentsForRunner(undefined)).toBe(true);
  });

  it('takes the permissive flag when one runner id serves several entries', () => {
    expect(scenarioCatalog.filter((scenario) => scenario.runnerId === 'TR-402').length).toBeGreaterThan(1);
    expect(allowRealAgentsForRunner('TR-402')).toBe(true);
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
