import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useHarnesses } from '../hooks/useHarnesses';
import { useHarnessModels, tierOverrides, tierLabel, type TierOverride } from '../hooks/useHarnessRoute';
import { ModelTier, type Harness, type HarnessModel } from '../types/generated';
import { useAutosaveSetting } from './SettingsAutosave';
import { TierMark } from './TierMark';
import './HarnessRoute.css';

const tiers = [ModelTier.Light, ModelTier.Standard, ModelTier.Deep];
const sameModel = (entry: TierOverride, harness: string, provider: string, model: string) => entry.harness === harness && entry.provider === provider && entry.model === model;

function ModelTierRow({ model, entries, setTier }: { model: HarnessModel; entries: TierOverride[]; setTier: (model: HarnessModel, tier?: ModelTier) => void }) {
  const own = entries.find(entry => sameModel(entry, model.harness, model.provider, model.id));
  const alias = model.tier_source === 'alias';
  const selected = alias ? undefined : own?.tier || model.shipped_tier;
  const name = [model.provider, model.name || model.id].filter(Boolean).join(' / ');
  return <div className={`model-tier-row ${own ? 'overridden' : ''}`}>
    <div>
      <span>{name}</span> <TierMark row tier={{ tier: selected, source: alias ? model.tier_source : own ? 'override' as typeof model.tier_source : model.tier_source }} />
      <div className="route-detail">{alias ? 'The harness decides this alias’s model.' : `${own ? 'Your tier · ' : ''}attn: ${model.shipped_tier || 'no tier'}`}{model.name !== model.id && ` · ${model.id}`}</div>
    </div>
    <div className="model-tier-controls">
      {!alias && <div className="model-tier-radios" role="radiogroup" aria-label={`Tier for ${name}`} onKeyDown={event => { if (event.key === 'Delete' || event.key === 'Backspace') { event.preventDefault(); setTier(model); } }}>
        {tiers.map(tier => <label key={tier}><input type="radio" name={`tier-${model.harness}-${model.provider}-${model.id}`} value={tier} checked={selected === tier} onChange={() => setTier(model, tier)} /><span>{tierLabel(tier)}</span></label>)}
      </div>}
      {own && <button type="button" className="settings-action quiet" aria-label={`Reset tier for ${name}`} onClick={() => setTier(model)}>Reset</button>}
    </div>
  </div>;
}

function HarnessTiers({ harness, entries, setTier, remove }: { harness: Harness; entries: TierOverride[]; setTier: (model: HarnessModel, tier?: ModelTier) => void; remove: (entry: TierOverride) => void }) {
  const { sendHarnessModels } = useDaemonApi();
  const view = useHarnessModels(harness, sendHarnessModels);
  const models = view.catalog?.models || [];
  const orphans = entries.filter(entry => entry.harness === harness.id && (!harness.discovery || view.catalog || view.error || !harness.available) && !models.some(model => model.id === entry.model && model.provider === entry.provider));
  const defaultName = (tier: ModelTier) => {
    if (view.loading) return 'Updating…';
    const id = view.catalog?.tier_defaults[tier];
    const model = models.find(candidate => candidate.id === id && candidate.tier === tier && candidate.access !== 'unsupported');
    return model?.name || model?.id || `none found, ${harness.name} decides`;
  };
  return <section className="settings-block" aria-label={`${harness.name} models`}>
    <div className="model-tier-header"><h3>{harness.name}</h3>{harness.discovery && <button type="button" className="settings-action quiet" disabled={view.loading} onClick={() => view.discover(true)}>Refresh</button>}</div>
    <div className="model-tier-defaults"><span>Light default → {defaultName(ModelTier.Light)}</span><span>Deep default → {defaultName(ModelTier.Deep)}</span></div>
    {view.error && <div className="settings-warning" role="alert">{view.error}</div>}
    {!harness.discovery && <p className="settings-description">This harness does not report a model catalog.</p>}
    {view.loading && !view.catalog && <p role="status">Discovering models…</p>}
    <div className="model-tier-list">{models.map(model => <ModelTierRow key={`${model.provider}/${model.id}`} model={model} entries={entries} setTier={setTier} />)}</div>
    {orphans.length > 0 && <div className="model-tier-orphans"><h4>{view.error || !harness.available ? 'Saved overrides' : 'No longer reported'}</h4>{orphans.map(entry => <div className="model-tier-row" key={`${entry.provider}/${entry.model}`}><div>{[entry.provider, entry.model].filter(Boolean).join(' / ')}<div className="route-detail">Your tier: {entry.tier}</div></div><button type="button" className="settings-action quiet" onClick={() => remove(entry)}>Remove</button></div>)}</div>}
  </section>;
}

export function ModelTierSettings() {
  const { settings, sendSaveSetting } = useDaemonApi();
  const { harnesses, loading, error, retry } = useHarnesses();
  const draft = useAutosaveSetting('model_tier_overrides', settings.model_tier_overrides || '[]', sendSaveSetting, raw => JSON.stringify(tierOverrides(raw)));
  const entries = tierOverrides(draft.value);
  const remove = (entry: TierOverride) => { void draft.apply(JSON.stringify(entries.filter(candidate => !sameModel(candidate, entry.harness, entry.provider, entry.model)))); };
  const setTier = (model: HarnessModel, tier?: ModelTier) => {
    const next = entries.filter(entry => !sameModel(entry, model.harness, model.provider, model.id));
    if (tier && tier !== model.shipped_tier) next.push({ harness: model.harness, provider: model.provider, model: model.id, tier });
    void draft.apply(JSON.stringify(next));
  };
  const missingHarnesses = entries.filter(entry => !harnesses.some(harness => harness.id === entry.harness));
  return <>
    <div className="settings-block-intro"><h3>Models</h3><p className="settings-description">Tiers describe how much intelligence a task needs. attn ships the mapping; your changes apply across profiles on this daemon. Reset returns a model to attn’s tier.</p></div>
    {error && <div className="settings-warning" role="alert">{error}<button type="button" className="settings-action" onClick={retry}>Retry</button></div>}
    {loading && !harnesses.length && <p role="status">Loading harnesses…</p>}
    {harnesses.map(harness => <HarnessTiers key={harness.id} harness={harness} entries={entries} setTier={setTier} remove={remove} />)}
    {missingHarnesses.length > 0 && <section className="settings-block"><h3>Harnesses no longer reported</h3>{missingHarnesses.map(entry => <div className="model-tier-row" key={`${entry.harness}/${entry.provider}/${entry.model}`}><span>{entry.harness} / {[entry.provider, entry.model].filter(Boolean).join(' / ')} · {entry.tier}</span><button type="button" className="settings-action quiet" onClick={() => remove(entry)}>Remove</button></div>)}</section>}
  </>;
}
