import { describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import { SessionProvenance } from '../components/SessionProvenance';
import { readProvenance } from './useUiAutomationBridge';

vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

const automation = {
  run_id: 'run-1',
  definition_id: 'slice4-sol',
  definition_name: 'Slice 4 packaged continuity proof',
  trigger_type: 'github_review_requested',
  pull_request: {
    repository: 'mock.github.local/owner/repo',
    number: 42,
    url: 'https://mock.github.local/owner/repo/pull/42',
    title: 'Automation live-test review',
    head_sha: 'a054675583e4d5a1206da85cb80642f8c0630126',
  },
};

// The sidebar renders badge/compact, the pane header and the automations run
// row render line; a reader that misses one of them reads nothing at all.
describe('readProvenance', () => {
  it.each(['badge', 'compact', 'line', 'detail'] as const)('reads the whole description at %s density', (density) => {
    const { container } = render(<SessionProvenance automation={automation} density={density} />);

    const description = readProvenance(container);
    expect(description).toContain('Slice 4 packaged continuity proof');
    expect(description).toContain('repo#42');
  });

  it('is empty when the session has no provenance to show', () => {
    const { container } = render(<SessionProvenance />);

    expect(readProvenance(container)).toBe('');
  });
});
