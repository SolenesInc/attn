import { afterEach, describe, expect, it, vi } from 'vitest';
import { persistSelectedSetupId, readSelectedSetupId, SELECTED_SETUP_STORAGE_KEY } from './selectedSetup';

describe('selected setup', () => {
  afterEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it('remembers a setup until it is forgotten', () => {
    expect(readSelectedSetupId()).toBeUndefined();
    persistSelectedSetupId('setup-1');
    expect(readSelectedSetupId()).toBe('setup-1');
    persistSelectedSetupId(undefined);
    expect(window.localStorage.getItem(SELECTED_SETUP_STORAGE_KEY)).toBeNull();
    expect(readSelectedSetupId()).toBeUndefined();
  });

  it('treats a blank stored value as nothing remembered', () => {
    window.localStorage.setItem(SELECTED_SETUP_STORAGE_KEY, '  ');
    expect(readSelectedSetupId()).toBeUndefined();
  });

  it('connects without a setup when storage is unreadable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(readSelectedSetupId()).toBeUndefined();
  });
});
