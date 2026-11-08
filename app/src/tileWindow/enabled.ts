import { isMacLikePlatform } from '../shortcuts/platform';
import { BoolSetting, isOn } from '../utils/settings';

export function tileWindowsEnabled(settings: Record<string, unknown>): boolean {
  return isMacLikePlatform() && isOn(settings, BoolSetting.tilePopoutEnabled);
}
