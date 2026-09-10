import { describe, expect, it } from 'vitest';
import { parseScenarioList, planSoak } from './plan-soak.mjs';

const catalog = [
  { id: 'terminal-annotations' },
  { id: 'terminal-block-resize' },
  { id: 'workspace-switching' },
];

describe('parseScenarioList', () => {
  it('trims ids and preserves their requested order', () => {
    expect(parseScenarioList(' terminal-block-resize,workspace-switching ', catalog)).toEqual([
      'terminal-block-resize',
      'workspace-switching',
    ]);
  });

  it('names unknown ids and the catalog', () => {
    expect(() => parseScenarioList('terminal-annotations,missing', catalog)).toThrow(
      'Unknown scenario id(s): missing\nKnown scenarios: terminal-annotations, terminal-block-resize, workspace-switching',
    );
  });

  it('rejects empty and duplicate entries', () => {
    expect(() => parseScenarioList('terminal-annotations,,workspace-switching', catalog)).toThrow(
      'Scenario ids must be a comma-separated list with no empty entries.',
    );
    expect(() => parseScenarioList('workspace-switching,workspace-switching', catalog)).toThrow(
      'Duplicate scenario id: workspace-switching',
    );
  });
});

describe('planSoak', () => {
  it('emits a GitHub matrix and numeric repeat', () => {
    expect(planSoak({ scenarios: 'terminal-annotations,workspace-switching', repeat: '10' }, catalog)).toEqual({
      matrix: { scenario: ['terminal-annotations', 'workspace-switching'] },
      repeat: 10,
    });
  });

  it.each(['', '0', '-1', '1.5', 'nope'])('rejects invalid repeat %j', (repeat) => {
    expect(() => planSoak({ scenarios: 'terminal-annotations', repeat }, catalog)).toThrow(
      `Repeat must be a positive integer, got: ${repeat}`,
    );
  });
});
