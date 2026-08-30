export function withNavigatorPlatform<T>(platform: string, fn: () => T): T {
  const nav = window.navigator as Navigator & { platform?: string };
  const original = nav.platform;
  Object.defineProperty(nav, 'platform', { value: platform, configurable: true });
  try {
    return fn();
  } finally {
    Object.defineProperty(nav, 'platform', { value: original, configurable: true });
  }
}
