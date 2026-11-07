import { vi } from 'vitest';

export const tauriFakes = {
  core: {
    invoke: vi.fn(async () => undefined),
    isTauri: vi.fn(() => false),
    convertFileSrc: vi.fn((filePath: string) => `asset://localhost/${filePath}`),
  },
  app: {
    getVersion: vi.fn(async () => '0.0.0'),
  },
  event: {
    emitTo: vi.fn(async () => undefined),
    emit: vi.fn(async () => {}),
    listen: vi.fn(async () => () => {}),
  },
  window: {
    getCurrentWindow: vi.fn(() => ({
      hide: vi.fn(async () => {}),
      isVisible: vi.fn(async () => true),
      isFullscreen: vi.fn(async () => false),
      onResized: vi.fn(async () => () => {}),
    })),
  },
  path: {
    downloadDir: vi.fn(async () => '/Users/me/Downloads'),
    homeDir: vi.fn(async () => '/Users/me'),
    join: vi.fn(async (...parts: string[]) => parts.join('/')),
  },
  deepLink: {
    onOpenUrl: vi.fn(async () => () => {}),
    getCurrent: vi.fn(async () => []),
  },
  opener: {
    openUrl: vi.fn(async () => {}),
    openPath: vi.fn(async () => {}),
    revealItemInDir: vi.fn(async () => {}),
  },
  dialog: {
    open: vi.fn(async () => null),
    save: vi.fn(async () => null),
  },
  fs: {
    BaseDirectory: { AppLocalData: 'AppLocalData' },
    exists: vi.fn(async () => false),
    mkdir: vi.fn(async () => {}),
    readTextFile: vi.fn(async () => ''),
    stat: vi.fn(async () => ({ size: 0 })),
    writeTextFile: vi.fn(async () => {}),
  },
};

export function resetTauriFakes() {
  for (const module of Object.values(tauriFakes)) {
    for (const member of Object.values(module)) {
      if (vi.isMockFunction(member)) member.mockReset();
    }
  }
}
