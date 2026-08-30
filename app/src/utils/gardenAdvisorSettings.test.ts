import { describe, expect, it } from 'vitest';
import {
  defaultGardenAdvisorConfig,
  parseGardenAdvisorSetting,
  serializeGardenAdvisorConfig,
} from './gardenAdvisorSettings';

describe('gardenAdvisorSettings', () => {
  it('falls back to the complete Codex default', () => {
    expect(parseGardenAdvisorSetting(undefined)).toEqual({
      agent: 'codex',
      model: 'gpt-5.6-luna',
      effort: 'xhigh',
    });
    expect(parseGardenAdvisorSetting('not json')).toEqual(defaultGardenAdvisorConfig());
  });

  it('fills per-agent defaults', () => {
    expect(parseGardenAdvisorSetting('{"agent":"claude"}')).toEqual({
      agent: 'claude',
      model: 'sonnet',
      effort: 'medium',
    });
    expect(parseGardenAdvisorSetting('{"agent":"copilot","effort":"high"}')).toEqual({
      agent: 'copilot',
      model: 'claude-sonnet-4.6',
      effort: '',
    });
  });

  it('omits unsupported empty effort', () => {
    expect(serializeGardenAdvisorConfig({
      agent: 'copilot',
      model: 'claude-sonnet-4.6',
      effort: '',
    })).toBe('{"agent":"copilot","model":"claude-sonnet-4.6"}');
  });
});
