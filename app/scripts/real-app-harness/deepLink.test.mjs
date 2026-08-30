import { describe, expect, it } from 'vitest';

import { deepLinkCommand } from './deepLink.mjs';

describe('deepLinkCommand', () => {
  it('routes through LaunchServices on macOS', () => {
    expect(deepLinkCommand('attn-dev://spawn?cwd=%2Ftmp', { platform: 'darwin' })).toEqual({
      command: 'open',
      args: ['attn-dev://spawn?cwd=%2Ftmp'],
    });
  });

  it('hands the URL to the profile app binary on Linux', () => {
    expect(deepLinkCommand('attn-dev://spawn?cwd=%2Ftmp', {
      platform: 'linux',
      appExecutable: '/home/u/.local/share/attn-dev/bin/attn-app',
    })).toEqual({
      command: '/home/u/.local/share/attn-dev/bin/attn-app',
      args: ['attn-dev://spawn?cwd=%2Ftmp'],
    });
  });

  it('says what it is missing when Linux has no executable to hand it to', () => {
    expect(() => deepLinkCommand('attn-dev://spawn', { platform: 'linux' }))
      .toThrow(/needs the app executable path/);
  });
});
