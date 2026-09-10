import { useAutosaveSetting, type SaveSetting } from './SettingsAutosave';
import { useCallback, useMemo, useState } from 'react';
import type { SessionAgent } from '../types/sessionAgent';
import { agentLabel } from '../utils/agentAvailability';
import {
  ACTIVITY_CONFIG_SETTING,
  ACTIVITY_ENABLED_SETTING,
  ACTIVITY_INTERVALS_SETTING,
  INTERVAL_MAX_SECONDS,
  INTERVAL_MIN_SECONDS,
  parseActivityConfigSetting,
  parseActivityIntervalsSetting,
} from '../utils/activitySettings';

const MODEL_PRESETS: Partial<Record<SessionAgent, { value: string; label: string }[]>> = {
  claude: [
    { value: 'claude-haiku-4-5', label: 'Haiku 4.5 (Recommended)' },
    { value: 'sonnet', label: 'Sonnet (Higher quality)' },
  ],
  codex: [
    { value: 'gpt-5.6-luna', label: 'gpt-5.6-luna (Recommended — fastest, cheapest)' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini' },
  ],
};

// Effort measured inert on Claude — none, low, medium and high all land within the same output-token band on identical input — so only Codex offers it.
const EFFORT_LEVELS: Partial<Record<SessionAgent, string[]>> = {
  codex: ['minimal', 'low', 'medium', 'high'],
};

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
  const [customModel, setCustomModel] = useState(
    Boolean(saved.model) && !(MODEL_PRESETS[saved.agent as SessionAgent] ?? []).some((p) => p.value === saved.model),
  );
  const updateConfig = (updates: Partial<typeof saved>, commit = true) => {
    const next = JSON.stringify({ agent, model, effort, ...updates });
    if (commit) void configDraft.apply(next); else configDraft.set(next);
  };
  const updateInterval = (key: string, value: string) => intervalsDraft.set(JSON.stringify({ watching, present, [key]: value }));

  const presets = agent ? MODEL_PRESETS[agent] ?? [] : [];
  const efforts = agent ? EFFORT_LEVELS[agent] ?? [] : [];

  const handleAgentChange = (next: SessionAgent | '') => {
    updateConfig({ agent: next, model: '', effort: '' });
    setCustomModel(false);
  };

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

        <div className="settings-field-grid two-column">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-agent">Agent</label>
            <select
              id="settings-activity-agent"
              data-testid="settings-activity-agent"
              className="settings-input"
              value={agent}
              onChange={(event) => handleAgentChange(event.target.value as SessionAgent | '')}
            >
              <option value="">Select an agent</option>
              {agents.map((option) => (
                <option key={option} value={option}>{agentLabel(option)}</option>
              ))}
            </select>
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-model">Model</label>
            <select
              id="settings-activity-model"
              data-testid="settings-activity-model"
              className="settings-input"
              value={customModel ? 'custom' : model}
              onChange={(event) => {
                const next = event.target.value;
                setCustomModel(next === 'custom');
                if (next !== 'custom') updateConfig({ model: next });
              }}
              disabled={!agent}
            >
              <option value="">Recommended default</option>
              {presets.map((preset) => (
                <option key={preset.value} value={preset.value}>{preset.label}</option>
              ))}
              <option value="custom">Custom…</option>
            </select>
          </div>
        </div>

        {agent && customModel && (
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-model-custom">Custom model</label>
            <input
              id="settings-activity-model-custom"
              data-testid="settings-activity-model-custom"
              type="text"
              className="settings-input"
              value={model}
              onChange={(event) => updateConfig({ model: event.target.value }, false)}
              onBlur={configDraft.onBlur}
              onKeyDown={configDraft.onKeyDown}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
          </div>
        )}

        {efforts.length > 0 && (
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-activity-effort">Reasoning effort</label>
            <select
              id="settings-activity-effort"
              data-testid="settings-activity-effort"
              className="settings-input"
              value={effort}
              onChange={(event) => updateConfig({ effort: event.target.value })}
            >
              <option value="">Recommended default</option>
              {efforts.map((level) => (
                <option key={level} value={level}>{level}</option>
              ))}
            </select>
          </div>
        )}

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
