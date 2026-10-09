import { useEffect, useSyncExternalStore } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import type { Harness, HarnessModel, ModelTier, ModelTierSource } from '../types/generated';
import type { HarnessModelCatalog } from './daemonDelegationEvents';

export type RouteHarness = Pick<Harness, 'id' | 'name' | 'available'> & Partial<Pick<Harness, 'model_pin' | 'effort_pin' | 'discovery'>>;
export interface HarnessRoute { harness: string; provider: string; model: string; effort: string }
export interface HarnessRouteRules {
  harnesses: RouteHarness[];
  allowNone?: boolean;
  fixedHarness?: boolean;
  requireAvailable?: boolean;
  effort?: (harness: string) => boolean;
  tier?: ModelTier;
  defaultEffort?: string | ((harness: string) => string);
  inherited?: HarnessRoute;
  noneLabel?: string;
}
export interface TierOverride { harness: string; provider: string; model: string; tier: ModelTier }
export interface TierInfo { tier?: ModelTier; source: ModelTierSource | 'none'; shipped?: ModelTier }
export interface RouteModelOption { model?: HarnessModel; stored?: boolean; label: string; group: string }

interface CatalogEntry { catalog?: HarnessModelCatalog; error: string; request?: Promise<void>; generation: number; executable: string }
const catalogs = new Map<string, CatalogEntry>();
const listeners = new Set<() => void>();
let version = 0;
let generation = 0;
let overridesValue: string | undefined;
const emit = () => { version++; for (const listener of listeners) listener(); };
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export function clearHarnessModelCatalogs() { generation++; catalogs.clear(); overridesValue = undefined; emit(); }
export const knownHarnessModel = (harness: string, provider: string, id: string) => catalogs.get(harness)?.catalog?.models.find(model => model.id === id && model.provider === provider);
export function useKnownModelName(harness: string, provider: string, id: string) {
  useSyncExternalStore(subscribe, () => version);
  return knownModelName(harness, provider, id);
}
export const knownModelName = (harness: string, provider: string, id: string) => catalogs.get(harness)?.catalog?.models.find(model => model.id === id && model.provider === provider)?.name || '';
export const tierOverrides = (raw: string | undefined): TierOverride[] => raw?.trim() ? (JSON.parse(raw) as TierOverride[] | null) || [] : [];

function discoverModels(harness: RouteHarness | undefined, load: (id: string, refresh?: boolean) => Promise<HarnessModelCatalog>, refresh = false, executable = '') {
  const id = harness?.id;
  if (!id || harness.discovery === false) return;
  const existing = catalogs.get(id);
  if (existing?.request && existing.generation === generation && existing.executable === executable) return;
  if (!refresh && existing?.generation === generation && existing.executable === executable && (existing.catalog || existing.error)) return;
  const current = generation;
  const entry: CatalogEntry = { catalog: existing?.executable === executable ? existing.catalog : undefined, error: '', generation: current, executable };
  const request = load(id, refresh).then(catalog => { if (current === generation) entry.catalog = catalog; })
    .catch((cause: unknown) => { if (current === generation) { entry.catalog = undefined; entry.error = cause instanceof Error ? cause.message : String(cause); } })
    .finally(() => { if (catalogs.get(id) === entry) { entry.request = undefined; emit(); } });
  entry.request = request;
  catalogs.set(id, entry);
  emit();
}

export function useHarnessModels(harness: RouteHarness | undefined, load: (id: string, refresh?: boolean) => Promise<HarnessModelCatalog>, onMount = true) {
  const { settings, isConnected } = useDaemonApi();
  const raw = settings.model_tier_overrides || '';
  const executable = settings[`${harness?.id}_executable`] || '';
  useSyncExternalStore(subscribe, () => version);
  const epoch = generation;
  useEffect(() => {
    if (overridesValue !== raw) { overridesValue = raw; generation++; emit(); }
    if (isConnected && (onMount || catalogs.has(harness?.id || ''))) discoverModels(harness, load, false, executable);
  }, [harness, load, onMount, raw, epoch, isConnected, executable]);
  const discover = (refresh = false) => { if (isConnected) discoverModels(harness, load, refresh, executable); };
  const cached = catalogs.get(harness?.id || '');
  const entry = harness?.discovery !== false && cached?.executable === executable ? cached : undefined;
  return { catalog: entry?.catalog, loading: Boolean(entry?.request), error: entry?.error || '', discover };
}

const tierRank = (tier: string | undefined) => ['light', 'standard', 'deep'].indexOf(tier || '');
export const tierLabel = (tier: string) => tier[0].toUpperCase() + tier.slice(1);
export const routeFromStored = (harness: string, model: string, effort: string): HarnessRoute => {
  const slash = model.indexOf('/');
  return { harness, provider: slash < 0 ? '' : model.slice(0, slash), model: slash < 0 ? model : model.slice(slash + 1), effort };
};
export const storedRouteModel = (route: HarnessRoute) => route.provider ? `${route.provider}/${route.model}` : route.model;

export function useHarnessRoute(input: HarnessRoute, rules: HarnessRouteRules, discovery: 'on-mount' | 'on-open' | 'never') {
  const { sendHarnessModels, settings } = useDaemonApi();
  const effectiveHarness = input.harness || rules.inherited?.harness || '';
  const inherited = rules.inherited?.harness === effectiveHarness ? rules.inherited : undefined;
  const harness = rules.harnesses.find(candidate => candidate.id === effectiveHarness);
  const models = useHarnessModels(harness, sendHarnessModels, discovery === 'on-mount');
  const catalog = models.catalog;
  const pinsUnavailable = Boolean(rules.requireAvailable && !harness?.available);
  const canPinModel = harness?.model_pin !== false && !pinsUnavailable;
  const selected = catalog?.models.find(model => (model.provider ? `${model.provider}/${model.id}` : model.id) === storedRouteModel(input));
  const value = selected ? { ...input, model: selected.id, provider: selected.provider } : input;
  const defaultID = rules.tier ? catalog?.tier_defaults[rules.tier] : undefined;
  const inheritedModel = !rules.tier && inherited?.model
    ? catalog?.models.find(model => model.id === inherited?.model && model.provider === inherited.provider) : undefined;
  const defaultModel = rules.tier
    ? catalog?.models.find(model => model.id === defaultID && model.tier === rules.tier && model.access !== 'unsupported')
    : inheritedModel;
  const runs = value.model ? selected : rules.tier || inherited?.model ? defaultModel : undefined;
  const override = value.model && !runs ? tierOverrides(settings.model_tier_overrides).find(entry => entry.harness === effectiveHarness && entry.provider === value.provider && entry.model === value.model) : undefined;
  const tier: TierInfo | undefined = runs ? { tier: runs.tier, source: runs.tier_source, shipped: runs.shipped_tier }
    : value.model && (override || catalog || models.error || harness?.discovery === false) ? { tier: override?.tier, source: override ? 'override' as ModelTierSource : 'none' } : undefined;
  const harnessName = harness?.name || effectiveHarness || 'None';
  const harnessLabel = !value.harness && effectiveHarness ? `${rules.noneLabel || 'Default'} → ${harnessName}` : harnessName;
  const unsetLabel = rules.tier ? `${tierLabel(rules.tier)} default → ${models.loading || (!catalog && !models.error && harness && harness.discovery !== false) ? 'checking models…' : defaultModel ? defaultModel.name || defaultModel.id : `none found, ${harnessName} decides`}` : inherited?.model ? `Launch default → ${inheritedModel?.name || storedRouteModel(inherited!)}` : `${harnessName} default`;
  const modelLabel = value.model ? selected?.name || storedRouteModel(value) : unsetLabel;
  const missing = Boolean(value.model && catalog && !selected);
  const effortModel = value.model ? selected : defaultModel;
  const effortMode = !effectiveHarness || harness?.effort_pin === false || rules.effort?.(effectiveHarness) === false ? 'hidden'
    : (!value.model && !rules.tier && !inherited?.model) || !harness || harness.discovery === false || Boolean(models.error) || missing || (Boolean(inherited?.model) && !value.model && !inheritedModel) ? 'free'
    : effortModel?.effort_support === 'unsupported' ? 'unsupported'
    : effortModel?.effort_levels.length ? 'levels' : catalog ? 'free' : 'pending';
  const effortLevels = effortModel?.effort_levels || [];
  const aboveTier = Boolean(rules.tier && tier?.tier && tierRank(tier.tier) > tierRank(rules.tier));
  const surfaceEffort = typeof rules.defaultEffort === 'function' ? rules.defaultEffort(effectiveHarness) : rules.defaultEffort || inherited?.effort;
  const defaultEffort = !value.effort && (effortLevels.includes(surfaceEffort || '') || effortMode === 'free') ? surfaceEffort : '';
  const options = (query = ''): RouteModelOption[] => {
    if (!canPinModel) return [];
    const aliases = (catalog?.models || []).filter(model => model.tier_source === 'alias');
    const order = [...new Set([rules.tier, 'deep', 'standard', 'light', 'no tier'].filter(Boolean))];
    const ordered: RouteModelOption[] = aliases.map(model => ({ model, label: model.name || model.id, group: '' }));
    for (const group of order) {
      for (const model of catalog?.models || []) {
        if (model.tier_source !== 'alias' && (model.tier || 'no tier') === group) ordered.push({ model, label: model.name || model.id, group: group || '' });
      }
      if (value.model && !selected && (tier?.tier || 'no tier') === group) ordered.push({ label: storedRouteModel(value), group: group || '', stored: true });
    }
    const terms = query.toLowerCase().trim().split(/\s+/);
    return ordered.filter(option => terms.every(term => `${option.label} ${option.model?.id || value.model} ${option.model?.provider || value.provider}`.toLowerCase().includes(term)));
  };
  const withModel = (model: HarnessModel | undefined): HarnessRoute => {
    const nextModel = model?.id || '', provider = model?.provider || '';
    if (nextModel === value.model && provider === value.provider && harness?.effort_pin !== false) return value;
    const levels = model?.effort_levels || defaultModel?.effort_levels || [];
    const support = (model || defaultModel)?.effort_support;
    const keepsEffort = harness?.effort_pin !== false && support !== 'unsupported' && (!levels.length || levels.includes(value.effort));
    return { ...value, model: nextModel, provider, effort: keepsEffort ? value.effort : '' };
  };
  return { ...models, value, rules, harness, pinsUnavailable, canPinModel, effectiveHarness, harnessName, harnessLabel, selected, tier, modelLabel, unsetLabel, missing, aboveTier, effortMode, effortLevels, defaultEffort, effortMissing: Boolean(value.effort && effortMode === 'levels' && !effortLevels.includes(value.effort)), options, withModel };
}
export type HarnessRouteView = ReturnType<typeof useHarnessRoute>;
