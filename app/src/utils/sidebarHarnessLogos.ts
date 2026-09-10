export const SIDEBAR_HARNESS_LOGOS_SETTING = 'sidebar_harness_logos_enabled';

export function areSidebarHarnessLogosEnabled(settings: Record<string, string>): boolean {
  return (settings[SIDEBAR_HARNESS_LOGOS_SETTING] || 'true') === 'true';
}
