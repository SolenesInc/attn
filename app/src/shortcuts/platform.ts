export function isMacLikePlatform(): boolean {
  if (typeof navigator === 'undefined') return false;

  const platform = String(navigator.platform || '').toLowerCase();
  if (platform.includes('mac')) return true;

  const ua = String(navigator.userAgent || '').toLowerCase();
  return ua.includes('mac os') || ua.includes('macintosh');
}

export function isAccelKeyPressed(e: KeyboardEvent): boolean {
  // Non-mac accepts Ctrl or Meta: CI/Playwright sends Meta even on Linux runners.
  return isMacLikePlatform() ? e.metaKey : e.ctrlKey || e.metaKey;
}
