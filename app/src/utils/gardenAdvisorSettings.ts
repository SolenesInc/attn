import type { SessionAgent } from '../types/sessionAgent';

export const GARDEN_ADVISOR_SETTING = 'garden.advisor';

export interface GardenAdvisorConfig {
  agent: SessionAgent;
  model: string;
  effort: string;
}

export function defaultGardenAdvisorConfig(agent: SessionAgent = 'codex'): GardenAdvisorConfig {
  return { agent: ['codex', 'claude', 'copilot'].includes(agent) ? agent : 'codex', model: '', effort: '' };
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
      effort: parsed.effort?.trim() || '',
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
