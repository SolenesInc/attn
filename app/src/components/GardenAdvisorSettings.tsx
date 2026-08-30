import { useCallback, useMemo, useState } from 'react';
import type { SessionAgent } from '../types/sessionAgent';
import { agentLabel } from '../utils/agentAvailability';
import {
  defaultGardenAdvisorConfig,
  GARDEN_ADVISOR_SETTING,
  parseGardenAdvisorSetting,
  serializeGardenAdvisorConfig,
} from '../utils/gardenAdvisorSettings';

const MODEL_PRESETS: Record<string, { value: string; label: string }[]> = {
  codex: [{ value: 'gpt-5.6-luna', label: 'gpt-5.6-luna (Recommended)' }],
  claude: [{ value: 'sonnet', label: 'Sonnet (Recommended)' }],
  copilot: [{ value: 'claude-sonnet-4.6', label: 'Sonnet (Recommended)' }],
};

const EFFORT_LEVELS: Record<string, string[]> = {
  codex: ['minimal', 'low', 'medium', 'high', 'xhigh'],
  claude: ['low', 'medium', 'high', 'xhigh', 'max'],
  copilot: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'],
};

interface GardenAdvisorSettingsProps {
  settings: Record<string, string>;
  agents: SessionAgent[];
  onSetSetting: (key: string, value: string) => void;
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
  const [agent, setAgent] = useState(saved.agent);
  const [model, setModel] = useState(saved.model);
  const [effort, setEffort] = useState(saved.effort);
  const [customModel, setCustomModel] = useState(
    !(MODEL_PRESETS[saved.agent] ?? []).some((preset) => preset.value === saved.model),
  );

  const presets = MODEL_PRESETS[agent] ?? [];
  const efforts = EFFORT_LEVELS[agent] ?? [];
  const available = settings[`${agent}_available`] !== 'false'
    && settings[`${agent}_cap_headless_task`] !== 'false';

  const changeAgent = useCallback((next: SessionAgent) => {
    const defaults = defaultGardenAdvisorConfig(next);
    setAgent(defaults.agent);
    setModel(defaults.model);
    setEffort(defaults.effort);
    setCustomModel(false);
  }, []);

  const save = useCallback(() => {
    onSetSetting(GARDEN_ADVISOR_SETTING, serializeGardenAdvisorConfig({ agent, model, effort }));
  }, [agent, effort, model, onSetSetting]);

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
          <div className="settings-warning" data-testid="settings-garden-advisor-unavailable">
            {agentLabel(agent)} is saved for Garden review but cannot run headless tasks here.
          </div>
        )}

        <div className="settings-field-grid two-column">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-garden-advisor-agent">Agent</label>
            <select
              id="settings-garden-advisor-agent"
              data-testid="settings-garden-advisor-agent"
              className="settings-input"
              value={agent}
              onChange={(event) => changeAgent(event.target.value)}
            >
              {agents.map((option) => (
                <option key={option} value={option}>{agentLabel(option)}</option>
              ))}
            </select>
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-garden-advisor-model">Model</label>
            <select
              id="settings-garden-advisor-model"
              data-testid="settings-garden-advisor-model"
              className="settings-input"
              value={customModel ? 'custom' : model}
              onChange={(event) => {
                const next = event.target.value;
                setCustomModel(next === 'custom');
                setModel(next === 'custom' ? '' : next);
              }}
            >
              {presets.map((preset) => (
                <option key={preset.value} value={preset.value}>{preset.label}</option>
              ))}
              <option value="custom">Custom...</option>
            </select>
          </div>
        </div>

        {customModel && (
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-garden-advisor-model-custom">
              Custom model
            </label>
            <input
              id="settings-garden-advisor-model-custom"
              data-testid="settings-garden-advisor-model-custom"
              type="text"
              className="settings-input"
              value={model}
              onChange={(event) => setModel(event.target.value)}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
          </div>
        )}

        {efforts.length > 0 && (
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-garden-advisor-effort">
              Reasoning effort
            </label>
            <select
              id="settings-garden-advisor-effort"
              data-testid="settings-garden-advisor-effort"
              className="settings-input"
              value={effort}
              onChange={(event) => setEffort(event.target.value)}
            >
              {agent === 'copilot' && <option value="">Recommended default</option>}
              {efforts.map((level) => (
                <option key={level} value={level}>{level}</option>
              ))}
            </select>
          </div>
        )}

        <div className="settings-row-inline">
          <button
            type="button"
            className="settings-action"
            data-testid="settings-garden-advisor-save"
            onClick={save}
          >
            Save
          </button>
        </div>
      </div>
    </section>
  );
}
