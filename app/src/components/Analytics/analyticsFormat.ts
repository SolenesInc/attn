import type { AnalyticsStatus } from '../../hooks/daemonAnalyticsEvents';
import type { AnalyticsBuild } from '../../types/generated';

export const clockTime = (iso: string) => {
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? '' : t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
};

export function age(iso: string, now: number): string {
  const seconds = Math.max(0, (now - Date.parse(iso)) / 1000);
  if (!Number.isFinite(seconds)) return '';
  if (seconds < 60) return 'now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

export function longAgo(iso: string, now: number): string {
  const short = age(iso, now);
  if (short === 'now' || short === '') return 'just now';
  const n = short.slice(0, -1);
  return { m: `${n} min ago`, h: `${n} h ago`, d: `${n} days ago` }[short.slice(-1)] ?? '';
}

export function when(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return iso;
  const day = t.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
  return `${day}, ${clockTime(iso)}`;
}

export function buildLabel(build: AnalyticsBuild): string {
  const channel = build.channel ? build.channel[0].toUpperCase() + build.channel.slice(1) : 'Build';
  const version = build.channel === 'source' && build.commit ? build.commit.slice(0, 7) : build.version;
  return `${channel} ${version}${build.dirty ? ', uncommitted changes' : ''}`;
}

export function settingName(key: string): string {
  const words = key.replace(/[._]enabled$/, '').replace(/[._]/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export const formatBytes = (n: number): string => {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
};

export function sharingLine(status: AnalyticsStatus | null): string {
  if (!status) return '';
  const sent = status.lastSentAt ? `Last sent ${clockTime(status.lastSentAt)}` : 'Nothing sent yet';
  return `${sent} · ${status.waiting.toLocaleString()} waiting`;
}

export const recordCount = (n: number) => `${n.toLocaleString()} record${n === 1 ? '' : 's'}`;
