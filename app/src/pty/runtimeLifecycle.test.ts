import { describe, expect, it } from 'vitest';
import {
  isAlreadyExistsError,
  normalizeAttachPolicy,
} from './runtimeLifecycle';

describe('runtimeLifecycle', () => {
  it('normalizes non-relaunch attach policies to same_app_remount', () => {
    expect(normalizeAttachPolicy('relaunch_restore')).toBe('relaunch_restore');
    expect(normalizeAttachPolicy('same_app_remount')).toBe('same_app_remount');
    expect(normalizeAttachPolicy('revive')).toBe('revive');
    expect(normalizeAttachPolicy('fresh_spawn')).toBe('same_app_remount');
    expect(normalizeAttachPolicy(undefined)).toBe('same_app_remount');
  });

  it('detects already-exists spawn errors', () => {
    expect(isAlreadyExistsError(new Error('Session already exists'))).toBe(true);
    expect(isAlreadyExistsError('session ALREADY EXISTS')).toBe(true);
    expect(isAlreadyExistsError(new Error('session missing'))).toBe(false);
  });

});
