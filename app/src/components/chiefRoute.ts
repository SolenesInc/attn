import { ModelTier } from '../types/generated';
import type { RouteHarness, HarnessRouteRules } from '../hooks/useHarnessRoute';

export const chiefHarnesses = (harnesses: RouteHarness[], settings: Record<string, string>) =>
  harnesses.filter(harness => ['claude', 'codex'].includes(harness.id) && harness.available
    && settings[`${harness.id}_cap_launch_instructions`] === 'true');

export const chiefRouteRules = (harnesses: RouteHarness[]): HarnessRouteRules => ({
  harnesses, requireAvailable: true, requireModel: true, tier: ModelTier.Deep, defaultEffort: 'low',
});
