import { describe, expect, it } from 'vitest';
import { daemonInstanceMatches, healthURLFromWS, instanceMismatchMessage } from './buildInstance';

describe('daemonInstanceMatches', () => {
  it('treats missing/empty instance as default', () => {
    // The default build expects "default", so missing → match.
    expect(daemonInstanceMatches(undefined)).toBe(true);
    expect(daemonInstanceMatches(null)).toBe(true);
    expect(daemonInstanceMatches('')).toBe(true);
    expect(daemonInstanceMatches('   ')).toBe(true);
    expect(daemonInstanceMatches('default')).toBe(true);
  });

  it('treats a non-default instance as mismatch for default build', () => {
    expect(daemonInstanceMatches('dev')).toBe(false);
    expect(daemonInstanceMatches('staging')).toBe(false);
  });
});

describe('healthURLFromWS', () => {
  it('rewrites ws → http and replaces path', () => {
    expect(healthURLFromWS('ws://127.0.0.1:29849/ws')).toBe('http://127.0.0.1:29849/health');
  });

  it('rewrites wss → https', () => {
    expect(healthURLFromWS('wss://example.com:443/ws')).toBe('https://example.com/health');
  });

  it('returns empty string on invalid input', () => {
    expect(healthURLFromWS('not a url')).toBe('');
  });
});

describe('instanceMismatchMessage', () => {
  it('mentions both expected and reported instances', () => {
    const msg = instanceMismatchMessage('dev');
    expect(msg).toContain('dev');
    expect(msg).toContain('default');
    expect(msg).toMatch(/refus/i);
  });

  it('uses "default" when the reported instance is missing', () => {
    const msg = instanceMismatchMessage(undefined);
    expect(msg).toMatch(/"default"/);
  });
});
