import type { SessionAgent } from '../types/sessionAgent';

export const GARDEN_ADVISOR_SETTING = 'garden.advisor';

export interface GardenAdvisorConfig {
  agent: SessionAgent;
  model: string;
  effort: string;
}

const DEFAULTS: Record<string, GardenAdvisorConfig> = {
  codex: { agent: 'codex', model: 'gpt-5.6-luna', effort: 'xhigh' },
  claude: { agent: 'claude', model: 'sonnet', effort: 'medium' },
  copilot: { agent: 'copilot', model: 'claude-sonnet-4.6', effort: '' },
};

export function defaultGardenAdvisorConfig(agent: SessionAgent = 'codex'): GardenAdvisorConfig {
  const defaults = DEFAULTS[agent] ?? DEFAULTS.codex;
  return { ...defaults };
}

export function parseGardenAdvisorSetting(raw: string | undefined): GardenAdvisorConfig {
  if (!raw?.trim()) return defaultGardenAdvisorConfig();
  try {
    const parsed = JSON.parse(raw) as { agent?: string; model?: string; effort?: string };
    const agent = parsed.agent?.trim().toLowerCase() || 'codex';
    const defaults = defaultGardenAdvisorConfig(agent);
    return {
      agent: defaults.agent,
      model: parsed.model?.trim() || defaults.model,
      effort: defaults.agent === 'copilot'
        ? ''
        : parsed.effort?.trim().toLowerCase() || defaults.effort,
    };
  } catch {
    return defaultGardenAdvisorConfig();
  }
}

export function serializeGardenAdvisorConfig(config: GardenAdvisorConfig): string {
  const serialized: Record<string, string> = {
    agent: config.agent,
    model: config.model.trim(),
  };
  if (config.effort.trim()) serialized.effort = config.effort.trim();
  return JSON.stringify(serialized);
}
