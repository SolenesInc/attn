export function relativeStamp(iso: string, now: Date): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  const seconds = Math.round((now.getTime() - at.getTime()) / 1000);
  if (seconds < 45) return 'now';
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))}m`;
  if (seconds < 86400 && sameDay(at, now)) return `${Math.round(seconds / 3600)}h`;
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  if (sameDay(at, yesterday)) return 'yesterday';
  if (seconds < 7 * 86400) return `${Math.round(seconds / 86400)}d`;
  return at.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

export function fullStamp(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString(undefined, {
    weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

export function tildePath(path: string): string {
  return path.replace(/^\/(?:Users|home)\/[^/]+(?=\/|$)/, '~');
}

export function shortPath(path: string): string {
  const tilde = tildePath(path);
  if (tilde.length <= 36) return tilde;
  const parts = tilde.split('/').filter(Boolean);
  return parts.length > 2 ? `…/${parts.slice(-2).join('/')}` : tilde;
}

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;

export function nameIds(text: string, label: (id: string) => string | undefined): string {
  return text
    .replace(/conversation [0-9a-f-]{36}/gi, 'its conversation')
    .replace(UUID, (id) => label(id) ?? 'a session');
}
