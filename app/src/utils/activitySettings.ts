// Kept out of SessionActivitySettings.tsx: a component file that also exports
// plain functions costs Fast Refresh its state preservation.
import type { SessionAgent } from '../types/sessionAgent';

export const ACTIVITY_ENABLED_SETTING = 'activity.enabled';
export const ACTIVITY_CONFIG_SETTING = 'activity.config';
export const ACTIVITY_INTERVALS_SETTING = 'activity.intervals';

export const INTERVAL_MIN_SECONDS = 30;
export const INTERVAL_MAX_SECONDS = 3600;

export interface ActivityConfig {
  agent: SessionAgent | '';
  model: string;
  effort: string;
}

export function parseActivityConfigSetting(raw: string | undefined): ActivityConfig {
  if (!raw?.trim()) return { agent: '', model: '', effort: '' };
  try {
    const parsed = JSON.parse(raw) as { agent?: string; model?: string; effort?: string };
    return {
      agent: (parsed.agent ?? '') as SessionAgent | '',
      model: parsed.model ?? '',
      effort: parsed.effort ?? '',
    };
  } catch {
    return { agent: '', model: '', effort: '' };
  }
}

export function parseActivityIntervalsSetting(raw: string | undefined): { watching: string; present: string } {
  if (raw?.trim()) {
    try {
      const parsed = JSON.parse(raw) as { watching?: number; present?: number };
      return {
        watching: String(parsed.watching ?? 120),
        present: String(parsed.present ?? 300),
      };
    } catch {
      /* a stored value that no longer parses shows the defaults, same as the daemon uses */
    }
  }
  return { watching: '120', present: '300' };
}

export function activityStaleMs(settings: Record<string, string>): number {
  const intervals = parseActivityIntervalsSetting(settings[ACTIVITY_INTERVALS_SETTING]);
  const seconds = Math.max(
    Number(intervals.watching) || 0,
    Number(intervals.present) || 0,
    INTERVAL_MIN_SECONDS,
  );
  return seconds * 3 * 1000;
}
