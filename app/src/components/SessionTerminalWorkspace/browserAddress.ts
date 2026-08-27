export function normalizeBrowserAddress(value: string): string {
  const trimmed = value.trim();
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)) return trimmed;
  const localHost = /^(?:localhost|127(?:\.\d{1,3}){3}|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(trimmed);
  return `${localHost ? 'http' : 'https'}://${trimmed}`;
}
