export function stubNavigatorPlatform(platform: string): () => void {
  const nav = window.navigator as Navigator & { platform?: string };
  const original = nav.platform;
  Object.defineProperty(nav, 'platform', { value: platform, configurable: true });
  return () => Object.defineProperty(nav, 'platform', { value: original, configurable: true });
}

export function withNavigatorPlatform<T>(platform: string, fn: () => T): T {
  const restore = stubNavigatorPlatform(platform);
  try {
    return fn();
  } finally {
    restore();
  }
}
