import type { EventMessage } from './protocol';
import type { ScriptedDaemon } from './scriptedDaemon';

type Model = EventMessage<'harness_models_result'>['models'][number];
type Tier = Model['tier'];
export const reportedModel = (harness: string, id: string, name: string, tier?: Tier, efforts: string[] = ['low', 'medium', 'high', 'xhigh', 'max']): Model => ({ harness, provider: '', id, name, description: '', detail: '', access: 'unknown', effort_support: efforts.length ? 'supported' : 'unsupported', effort_levels: efforts, tier, shipped_tier: tier, tier_source: id === 'default' ? 'alias' : tier ? 'shipped' : 'none' });
export const harnesses = [
  { id: 'claude', name: 'Claude', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'copilot', name: 'Copilot', available: true, model_pin: true, effort_pin: true, discovery: false },
  { id: 'pi', name: 'Pi', available: true, model_pin: true, effort_pin: true, discovery: true },
];
export const catalogs: Record<string, Model[]> = {
  claude: [reportedModel('claude', 'default', 'Default (recommended)'), reportedModel('claude', 'opus', 'Opus 5.5', 'deep'), reportedModel('claude', 'sonnet', 'Sonnet 5.5', 'standard'), reportedModel('claude', 'haiku', 'Haiku 5.5', 'light')],
  codex: [reportedModel('codex', 'gpt-6.1-sol', 'gpt-6.1-sol', 'deep', ['low', 'medium', 'high', 'xhigh', 'max', 'ultra']), reportedModel('codex', 'gpt-6-astra', 'gpt-6-astra', 'deep'), reportedModel('codex', 'gpt-6-luna', 'gpt-6-luna', 'light'), reportedModel('codex', 'gpt-5.6-terra', 'gpt-5.6-terra', 'standard'), reportedModel('codex', 'gpt-5.6-luna', 'gpt-5.6-luna', 'light'), reportedModel('codex', 'new-model', 'New model', undefined)],
  pi: [],
};
export function serveHarnessCatalogs(daemon: ScriptedDaemon, reportedHarnesses = harnesses) {
  daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: true, preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, harnesses: reportedHarnesses, templates: [], expanded_roles: [] }));
  daemon.on('harness_models', ({ harness }) => {
    const overrides = JSON.parse(daemon.settings.model_tier_overrides || '[]') as Array<{ harness: string; provider: string; model: string; tier: Tier }>;
    const models = (catalogs[harness] || []).map(model => {
      const own = overrides.find(entry => entry.harness === harness && entry.provider === model.provider && entry.model === model.id);
      return own && model.tier_source !== 'alias' ? { ...model, tier: own.tier, tier_source: 'override' as const } : model;
    });
    const tier_defaults: EventMessage<'harness_models_result'>['tier_defaults'] = {};
    for (const tier of ['light', 'standard', 'deep'] as const) {
      const model = models.find(candidate => candidate.tier === tier && candidate.access !== 'unsupported');
      if (model) tier_defaults[tier] = model.id;
    }
    return { event: 'harness_models_result', success: true, models, tier_defaults, detail: 'Reported by the test harness' };
  });
}
