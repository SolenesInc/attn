import { describe, expect, it } from 'vitest';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';

describe('assertFreshWorldTargetSafe', () => {
  it('throws when profile is empty (the prod profile string)', () => {
    expect(() => assertFreshWorldTargetSafe({ profile: '', appPath: '/Users/victor/Applications/attn-dev.app' }))
      .toThrow(/profile/i);
  });

  it("throws when profile is 'default' even with a non-prod appPath", () => {
    expect(() => assertFreshWorldTargetSafe({ profile: 'default', appPath: '/Users/victor/Applications/attn-fxm1.app' }))
      .toThrow(/default/i);
  });

  it('throws when appPath is missing', () => {
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1', appPath: '' }))
      .toThrow(/appPath/i);
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1' }))
      .toThrow(/appPath/i);
  });

  it('throws for a production-shaped target on a named profile (defense in depth)', () => {
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1', appPath: '/Users/victor/Applications/attn.app' }))
      .toThrow();
  });

  it('does not throw for a realistic named-profile target', () => {
    expect(() => assertFreshWorldTargetSafe({ profile: 'fxm1', appPath: '/Users/victor/Applications/attn-fxm1.app' }))
      .not.toThrow();
  });

  it("does not throw for the 'dev' profile", () => {
    expect(() => assertFreshWorldTargetSafe({ profile: 'dev', appPath: '/Users/victor/Applications/attn-dev.app' }))
      .not.toThrow();
  });
});
