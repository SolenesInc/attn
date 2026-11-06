import { DEFAULT_ON_SETTINGS, type BoolSettingKey } from '../types/settings.generated';

export { BoolSetting } from '../types/settings.generated';

export function isOn(settings: Record<string, unknown>, key: BoolSettingKey): boolean {
  const value = settings[key];
  return value ? value === 'true' : DEFAULT_ON_SETTINGS.has(key);
}
