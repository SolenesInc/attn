import { useAutosaveSetting, type SaveSetting } from './SettingsAutosave';
import { useCallback, useMemo } from 'react';
import type { SessionAgent } from '../types/sessionAgent';
import { useHeadlessHarnesses } from '../hooks/useHarnesses';
import { HarnessRouteChip } from './HarnessRouteChip';
import { ModelTier } from '../types/generated';
import {
  ACTIVITY_CONFIG_SETTING,
  ACTIVITY_ENABLED_SETTING,
  ACTIVITY_INTERVALS_SETTING,
  INTERVAL_MAX_SECONDS,
  INTERVAL_MIN_SECONDS,
  parseActivityConfigSetting,
  parseActivityIntervalsSetting,
} from '../utils/activitySettings';

interface SessionActivitySettingsProps {
  settings: Record<string, string>;
  agents: SessionAgent[];
  onSetSetting: SaveSetting;
}

export function SessionActivitySettings({
  settings,
  agents,
  onSetSetting,
}: SessionActivitySettingsProps) {
  const saved = useMemo(() => parseActivityConfigSetting(settings[ACTIVITY_CONFIG_SETTING]), [settings]);
  const savedIntervals = useMemo(
    () => parseActivityIntervalsSetting(settings[ACTIVITY_INTERVALS_SETTING]),
    [settings],
  );
  const enabled = (settings[ACTIVITY_ENABLED_SETTING] || 'false') === 'true';

  const configDraft = useAutosaveSetting(ACTIVITY_CONFIG_SETTING, JSON.stringify(saved), onSetSetting, (raw) => {
    const value = JSON.parse(raw) as typeof saved;
    if (!value.agent) return '';
    return JSON.stringify({ agent: value.agent, ...(value.model.trim() && { model: value.model.trim() }), ...(value.effort && { effort: value.effort }) });
  });
  const { agent, model, effort } = JSON.parse(configDraft.value) as typeof saved;
  const intervalsDraft = useAutosaveSetting(ACTIVITY_INTERVALS_SETTING, JSON.stringify(savedIntervals), onSetSetting, (raw) => {
    const values = JSON.parse(raw) as { watching: string; present: string };
    const result = { watching: Number(values.watching), present: Number(values.present) };
    for (const [name, value] of Object.entries(result)) {
      if (!Number.isInteger(value) || value < INTERVAL_MIN_SECONDS || value > INTERVAL_MAX_SECONDS) {
        throw new Error(`${name} refresh must be a whole number from ${INTERVAL_MIN_SECONDS} to ${INTERVAL_MAX_SECONDS} seconds; entered ${values[name as keyof typeof values] || 'empty'}.`);
      }
    }
    return JSON.stringify(result);
  });
  const { watching, present } = JSON.parse(intervalsDraft.value) as typeof savedIntervals;
  const updateConfig = (updates: Partial<typeof saved>, commit = true) => {
    const next = JSON.stringify({ agent, model, effort, ...updates });
    if (commit) void configDraft.apply(next); else configDraft.set(next);
  };
  const updateInterval = (key: string, value: string) => intervalsDraft.set(JSON.stringify({ watching, present, [key]: value }));

  const { harnesses, error, retry } = useHeadlessHarnesses(agents, settings);
  const toggle = useCallback(() => {
    onSetSetting(ACTIVITY_ENABLED_SETTING, enabled ? 'false' : 'true');
  }, [enabled, onSetSetting]);

  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Agents</div>
        <h3>Session activity</h3>
        <p className="settings-description">
          Summarize each session on Home using its transcript. This sends transcript excerpts
          to the selected model and incurs usage costs. Updates run only while you use attn.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Generate activity lines</p>
            <p className="settings-row-copy">
              Off by default. Choose an agent below to enable.
            </p>
          </div>
          <button
            type="button"
            className="settings-action"
            data-testid="settings-activity-toggle"
            onClick={toggle}
            disabled={!enabled && !saved.agent}
            title={!enabled && !saved.agent ? 'Choose an agent first' : undefined}
          >
            {enabled ? 'Disable' : 'Enable'}
          </button>
        </div>

        {agents.length === 0 && (
          <div className="settings-warning">No installed agent supports scoped headless tasks.</div>
        )}

        {error && <div className="settings-warning" role="alert">{error}<button type="button" className="settings-action quiet" onClick={retry}>Retry</button></div>}
        <HarnessRouteChip variant="field" aria-label="Session activity model" data-testid="settings-activity-route" value={{ harness: agent, provider: '', model, effort }} rules={{ requireAvailable: true, harnesses, allowNone: true, tier: ModelTier.Light, defaultEffort: 'low', effort: harness => harness !== 'claude' }} onChange={route => updateConfig({ agent: route.harness, model: route.model, effort: route.effort })} />

        <div className="settings-field-grid two-column">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-watching">
              Refresh on home (seconds)
            </label>
            <input
              id="settings-activity-watching"
              data-testid="settings-activity-watching"
              type="number"
              min={INTERVAL_MIN_SECONDS}
              max={INTERVAL_MAX_SECONDS}
              className="settings-input"
              value={watching}
              onChange={(event) => updateInterval('watching', event.target.value)}
              onBlur={intervalsDraft.onBlur}
              onKeyDown={intervalsDraft.onKeyDown}
            />
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-present">
              Refresh elsewhere in the app (seconds)
            </label>
            <input
              id="settings-activity-present"
              data-testid="settings-activity-present"
              type="number"
              min={INTERVAL_MIN_SECONDS}
              max={INTERVAL_MAX_SECONDS}
              className="settings-input"
              value={present}
              onChange={(event) => updateInterval('present', event.target.value)}
              onBlur={intervalsDraft.onBlur}
              onKeyDown={intervalsDraft.onKeyDown}
            />
          </div>
        </div>

        <div className="settings-hint">
          A session that has written nothing since its last line is skipped, so blocked and
          finished agents cost nothing however long home stays open.
        </div>
      </div>
    </section>
  );
}
