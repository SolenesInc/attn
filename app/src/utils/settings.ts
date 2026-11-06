import { DEFAULT_ON_SETTINGS, FEATURES, type BoolSettingKey } from '../types/settings.generated';

export { BoolSetting, FEATURES } from '../types/settings.generated';
export type { BoolSettingKey, Feature } from '../types/settings.generated';

export function isOn(settings: Record<string, unknown>, key: BoolSettingKey): boolean {
  const value = settings[key];
  return value ? value === 'true' : DEFAULT_ON_SETTINGS.has(key);
}

export const EXPERIMENTAL_FEATURES = FEATURES.filter(feature => feature.stage === 'experimental');
