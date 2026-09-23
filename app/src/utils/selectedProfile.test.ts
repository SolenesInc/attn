import { afterEach, describe, expect, it, vi } from 'vitest';
import { persistSelectedProfileId, readSelectedProfileId, SELECTED_PROFILE_STORAGE_KEY } from './selectedProfile';

describe('selected profile', () => {
  afterEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it('remembers a profile until it is forgotten', () => {
    expect(readSelectedProfileId()).toBeUndefined();
    persistSelectedProfileId('profile-1');
    expect(readSelectedProfileId()).toBe('profile-1');
    persistSelectedProfileId(undefined);
    expect(window.localStorage.getItem(SELECTED_PROFILE_STORAGE_KEY)).toBeNull();
    expect(readSelectedProfileId()).toBeUndefined();
  });

  it('treats a blank stored value as nothing remembered', () => {
    window.localStorage.setItem(SELECTED_PROFILE_STORAGE_KEY, '  ');
    expect(readSelectedProfileId()).toBeUndefined();
  });

  it('connects without a profile when storage is unreadable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(readSelectedProfileId()).toBeUndefined();
  });
});
