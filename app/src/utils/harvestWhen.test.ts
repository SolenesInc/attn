import { describe, it, expect } from 'vitest';
import { harvestWhenDisplay } from './harvestWhen';

describe('harvestWhenDisplay', () => {
  it('drops the host from the session pull request id and says the condition', () => {
    const display = harvestWhenDisplay({
      pull_request: 'github.com:victorarias/attn#123',
      url: 'https://github.com/victorarias/attn/pull/123',
      set_at: '2026-09-02T10:00:00Z',
    });

    expect(display).toEqual({
      pullRequest: 'victorarias/attn#123',
      sentence: 'harvests when victorarias/attn#123 merges',
      marker: 'harvests on #123',
      url: 'https://github.com/victorarias/attn/pull/123',
    });
  });

  it('has nothing to show for a seed nobody armed', () => {
    expect(harvestWhenDisplay(undefined)).toBeNull();
    expect(harvestWhenDisplay({ pull_request: '  ', url: '', set_at: '' })).toBeNull();
  });

  it('keeps an id it cannot take apart rather than showing nothing', () => {
    const display = harvestWhenDisplay({ pull_request: 'somewhere-else', url: '', set_at: '' });

    expect(display?.pullRequest).toBe('somewhere-else');
    expect(display?.marker).toBe('harvests on somewhere-else');
    expect(display?.url).toBe('');
  });
});
