import { useState } from 'react';
import type { GuardianSelection, DelegationHarness, DelegationModel } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { AutoModePolicy } from '../hooks/useAutoModePolicy';
import { useDelegationModelCatalog } from '../hooks/useDelegationModelCatalog';

const pi: DelegationHarness = { id: 'pi', name: 'Pi', available: true, discovery: true, model_pin: true, effort_pin: true };
const modelKey = (provider: string, model: string) => JSON.stringify([provider, model]);
const followEfforts = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
const modelEfforts = (model: DelegationModel | undefined) => model?.effort_support === 'unsupported' ? ['off'] : model?.effort_levels ?? [];

export function GuardianSettings({ value = {}, policy, loadModels }: {
  value?: GuardianSelection;
  policy: Pick<AutoModePolicy, 'editing' | 'setPolicy'>;
  loadModels: (harness: string) => Promise<DelegationModelCatalog>;
}) {
  const { catalog, loading, error, discover } = useDelegationModelCatalog(pi, loadModels);
  const [failure, setFailure] = useState('');
  const models = catalog?.models ?? [];
  const selected = models.find(model => model.provider === value.provider && model.id === value.model);
  const key = value.provider && value.model ? modelKey(value.provider, value.model) : '';
  const efforts = key ? modelEfforts(selected) : followEfforts;
  const busy = policy.editing !== null;
  const save = async (selection: GuardianSelection) => {
    setFailure('');
    try { await policy.setPolicy({ guardian: selection }); }
    catch (error) { setFailure(error instanceof Error ? error.message : 'Could not save the guardian default'); }
  };
  const chooseModel = (key: string) => {
    const model = models.find(model => modelKey(model.provider, model.id) === key);
    if (!key) { void save({ effort: value.effort }); return; }
    if (!model) return;
    const supported = modelEfforts(model);
    void save({ provider: model.provider, model: model.id, effort: value.effort && supported.includes(value.effort) ? value.effort : undefined });
  };
  return <div className="automode-editor" data-testid="guardian-settings">
    <div className="automode-section-head">
      <h4>Guardian</h4>
      <p className="settings-description">The model that answers Pi approval requests when auto mode is on. Changes apply to new agent launches. Override it for one session in /security.</p>
    </div>
    <GuardianModelSelect models={models} selection={value} selectedKey={key} unavailable={Boolean(key && !selected)} loading={loading} busy={busy} onChange={chooseModel} />
    <GuardianEffortSelect value={value.effort ?? ''} efforts={efforts} busy={busy} onChange={effort => void save({ ...value, effort })} />
    {!key && <p className="settings-description">An explicit reasoning level must be supported by the session model. Otherwise the agent pauses and asks you to choose another level.</p>}
    <p className="settings-description">Default reasoning uses the provider default when low is unsupported. Selecting a model that cannot use the saved level resets reasoning to Default.</p>
    {loading && <p role="status">Reading Pi models…</p>}
    {catalog?.detail && <p className="settings-description">{catalog.detail}</p>}
    {error && <p className="settings-warning" role="alert">{error}</p>}
    {failure && <p className="settings-warning" role="alert">{failure}</p>}
    <div className="automode-model-actions">
      <button type="button" className="settings-action" disabled={loading || busy} onClick={() => discover(true)}>Refresh models</button>
      <button type="button" className="settings-action" disabled={busy || (!key && !value.effort)} onClick={() => void save({})}>Reset guardian default</button>
    </div>
  </div>;
}

function GuardianModelSelect({ models, selection, selectedKey, unavailable, loading, busy, onChange }: {
  models: DelegationModel[];
  selection: GuardianSelection;
  selectedKey: string;
  unavailable: boolean;
  loading: boolean;
  busy: boolean;
  onChange: (key: string) => void;
}) {
  return <>
    <label className="automode-field">
      <span className="automode-field-label">Model</span>
      <select className="settings-input" aria-label="Guardian model" value={selectedKey} disabled={busy} onChange={event => onChange(event.target.value)}>
        <option value="">Follow session model</option>
        {unavailable && <option value={selectedKey}>{selection.provider}/{selection.model} {loading ? '(checking)' : '(unavailable)'}</option>}
        {[...new Set(models.map(model => model.provider))].sort().map(provider => <optgroup key={provider} label={provider}>
          {models.filter(model => model.provider === provider).map(model => <option key={model.id} value={modelKey(provider, model.id)} disabled={model.access === 'unsupported'}>{model.name || model.id}</option>)}
        </optgroup>)}
      </select>
    </label>
    {unavailable && !loading && <p className="settings-warning">The saved guardian model is unavailable. Choose another model or restore Follow session model.</p>}
  </>;
}

function GuardianEffortSelect({ value, efforts, busy, onChange }: {
  value: string;
  efforts: string[];
  busy: boolean;
  onChange: (effort: string) => void;
}) {
  const unavailable = Boolean(value && !efforts.includes(value));
  return <>
    <label className="automode-field">
      <span className="automode-field-label">Reasoning</span>
      <select className="settings-input" aria-label="Guardian reasoning" value={value} disabled={busy} onChange={event => onChange(event.target.value)}>
        <option value="">Default (low when supported)</option>
        {unavailable && <option value={value}>{value} (unavailable)</option>}
        {efforts.map(effort => <option key={effort} value={effort}>{effort}</option>)}
      </select>
    </label>
    {unavailable && <p className="settings-warning">This model no longer supports the saved reasoning level. Choose another level before launching an agent.</p>}
  </>;
}
