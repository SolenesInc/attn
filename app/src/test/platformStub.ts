/** Runs `fn` with navigator.platform pinned, so platform-dependent shortcut code can be
 *  exercised for both macOS and Linux from the one suite. */
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
