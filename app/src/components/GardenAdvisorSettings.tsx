import { useAutosaveSetting, type SaveSetting } from './SettingsAutosave';
import { useMemo } from 'react';
import type { SessionAgent } from '../types/sessionAgent';
import { useHeadlessHarnesses } from '../hooks/useHarnesses';
import { HarnessRouteChip } from './HarnessRouteChip';
import { ModelTier } from '../types/generated';
import { agentLabel } from '../utils/agentAvailability';
import {
  GARDEN_ADVISOR_SETTING,
  type GardenAdvisorConfig,
  parseGardenAdvisorSetting,
  serializeGardenAdvisorConfig,
} from '../utils/gardenAdvisorSettings';

function serializeAdvisorDraft(draft: string): string {
  return serializeGardenAdvisorConfig(JSON.parse(draft) as GardenAdvisorConfig);
}

interface GardenAdvisorSettingsProps {
  settings: Record<string, string>;
  agents: SessionAgent[];
  onSetSetting: SaveSetting;
}

export function GardenAdvisorSettings({
  settings,
  agents,
  onSetSetting,
}: GardenAdvisorSettingsProps) {
  const saved = useMemo(
    () => parseGardenAdvisorSetting(settings[GARDEN_ADVISOR_SETTING]),
    [settings],
  );
  const draft = useAutosaveSetting(GARDEN_ADVISOR_SETTING, JSON.stringify(saved), onSetSetting, serializeAdvisorDraft);
  const { agent, model, effort } = JSON.parse(draft.value) as typeof saved;
  const update = (updates: Partial<typeof saved>, commit = true) => {
    const next = JSON.stringify({ agent, model, effort, ...updates });
    if (commit) void draft.apply(next); else draft.set(next);
  };
  const { harnesses, error, retry } = useHeadlessHarnesses(agents, settings);
  const available = settings[`${agent}_available`] !== 'false'
    && settings[`${agent}_cap_headless_task`] !== 'false';

  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Garden</div>
        <h3>Garden advisor</h3>
        <p className="settings-description">
          Classifies seeds during Review garden and can draft a handoff. It runs in the
          background with this saved agent and cannot change a seed.
        </p>
      </div>
      <div className="settings-block-body">
        {!available && (
          <div className="settings-warning">
            {agentLabel(agent)} is saved for Garden review but cannot run headless tasks here.
          </div>
        )}

        {error && <div className="settings-warning" role="alert">{error}<button type="button" className="settings-action quiet" onClick={retry}>Retry</button></div>}
        <HarnessRouteChip variant="field" aria-label="Garden advisor model" data-testid="settings-garden-advisor-route" value={{ harness: agent, provider: '', model, effort }} rules={{ requireAvailable: true, harnesses, tier: ModelTier.Light, defaultEffort: harness => harness === 'codex' ? 'xhigh' : harness === 'claude' ? 'medium' : '' }} onChange={route => update({ agent: route.harness, model: route.model, effort: route.effort })} />

      </div>
    </section>
  );
}
