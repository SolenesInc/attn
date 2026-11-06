import { BoolSetting, isOn } from './settings';
export const SIDEBAR_HARNESS_LOGOS_SETTING = 'sidebar_harness_logos_enabled';

export function areSidebarHarnessLogosEnabled(settings: Record<string, string>): boolean {
  return isOn(settings, BoolSetting.sidebarHarnessLogosEnabled);
}
