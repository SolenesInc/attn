import { useEffect, useRef, useState, useId } from 'react';
import type { DelegationChoice, DelegationPreferences, DelegationRole, DelegationSelection, DelegationHarness } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { DelegationPreferencesPolicy } from '../hooks/useDelegationPreferences';
import { DelegationRoleIcon } from './DelegationRoleIcon';
import './DelegationSettings.css';

const emptySelection = (): DelegationSelection => ({ harness: '', provider: '', model: '', effort: '' });
const id = (prefix: string) => `${prefix}-${crypto.randomUUID()}`;
const route = (s: DelegationSelection) => !s.harness ? 'Not configured' : [s.harness, s.provider, s.model || 'Harness default', s.effort].filter(Boolean).join(' / ');
const DELEGATION_ICONS = ['search', 'diamond', 'code', 'arrow', 'list', 'bug', 'spark', 'circle'] as const;

type SelectionPickerProps = {
  value: DelegationSelection;
  onChange: (s: DelegationSelection) => void;
  harnesses: DelegationHarness[];
  catalog?: DelegationModelCatalog;
  loading: boolean;
  error?: string;
  discover: (harness: string) => void;
};

const modelKey = (provider: string, model: string) => JSON.stringify([provider, model]);

function ModelField({ value, onChange, catalog, disabled, manual, setManual }: {
  value: DelegationSelection;
  onChange: (s: DelegationSelection) => void;
  catalog?: DelegationModelCatalog;
  disabled: boolean;
  manual: boolean;
  setManual: (manual: boolean) => void;
}) {
  const selected = catalog?.models.find(model => model.id === value.model && model.provider === value.provider);
  const selectedKey = modelKey(value.provider, value.model);
  const choose = (key: string) => {
    if (key === '__custom') { setManual(true); return; }
    if (key === modelKey('', '')) { onChange({ ...value, provider: '', model: '', effort: '' }); return; }
    const model = catalog?.models.find(candidate => modelKey(candidate.provider, candidate.id) === key);
    if (model) onChange({ ...value, provider: model.provider, model: model.id, effort: '' });
  };
  return <label>Model<span className="delegation-select"><select value={manual ? '__custom' : selectedKey} disabled={disabled} onChange={e => choose(e.target.value)}>
    <option value={modelKey('', '')}>Harness default</option>
    {catalog?.models.map(model => <option key={`${model.provider}/${model.id}`} value={modelKey(model.provider, model.id)} disabled={model.access === 'unsupported'}>{model.provider ? `${model.provider} / ` : ''}{model.name || model.id}</option>)}
    {value.model && !selected && <option value={selectedKey}>{value.provider ? `${value.provider} / ` : ''}{value.model} (custom)</option>}
    <option value="__custom">Enter a model ID…</option>
  </select></span></label>;
}

function SelectionHelp({ harness, model, levels, catalog, loading, error, discover }: {
  harness?: DelegationHarness;
  model?: DelegationModelCatalog['models'][number];
  levels: string[];
  catalog?: DelegationModelCatalog;
  loading: boolean;
  error?: string;
  discover: (harness: string) => void;
}) {
  let hint = '';
  if (levels.length) hint = `Supported effort: ${levels.join(', ')}.`;
  if (model?.effort_support === 'unsupported') hint = 'This model does not support an effort override.';
  if (harness?.model_pin === false) hint = 'This harness uses its own selected model.';
  return <>
    <div className="delegation-row">
      {hint && <p className="settings-hint">{hint}</p>}
      <button className="settings-action" disabled={!harness || loading} onClick={() => harness && discover(harness.id)}>{loading ? 'Discovering…' : catalog ? 'Refresh models' : 'Discover models'}</button>
    </div>
    {error && <p className="settings-warning" role="alert">{error}</p>}
    {catalog?.detail && <p className="settings-hint">{catalog.detail}</p>}
    {harness && !harness.available && <p className="settings-warning">This harness is unavailable on this daemon. Check Agents and models.</p>}
  </>;
}

function SelectionPicker({ value, onChange, harnesses, catalog, loading, error, discover }: SelectionPickerProps) {
  const prefix = useId();
  const [manual, setManual] = useState(false);
  const harness = harnesses.find(h => h.id === value.harness);
  const model = catalog?.models.find(m => m.id === value.model && m.provider === value.provider);
  const levels = model?.effort_levels ?? [];
  return <div className="delegation-picker">
    <div className="delegation-fields">
      <label>Harness<span className="delegation-select"><select value={value.harness} onChange={e => { setManual(false); onChange({ ...emptySelection(), harness: e.target.value }); }}>
        <option value="">Choose a harness</option>
        {harnesses.map(h => <option value={h.id} key={h.id}>{h.name}{h.available ? '' : ' (unavailable)'}</option>)}
        {value.harness && !harness && <option>{value.harness}</option>}
      </select></span></label>
      <ModelField value={value} onChange={onChange} catalog={catalog} disabled={!value.harness || harness?.model_pin === false} manual={manual} setManual={setManual} />
      <label>Effort<input list={`${prefix}-efforts`} value={value.effort} placeholder="Harness default" disabled={!value.harness || harness?.effort_pin === false || model?.effort_support === 'unsupported'} onChange={e => onChange({ ...value, effort: e.target.value })} />
        <datalist id={`${prefix}-efforts`}>{levels.map(level => <option key={level} value={level} />)}</datalist>
      </label>
    </div>
    {manual && <div className="delegation-fields custom-model">
      <label>Exact model ID<input value={value.model} onChange={e => onChange({ ...value, model: e.target.value, effort: '' })} placeholder="Model ID from your harness" /></label>
      {!['claude', 'codex', 'copilot'].includes(value.harness) && <label>Provider<input value={value.provider} onChange={e => onChange({ ...value, provider: e.target.value })} /></label>}
      <button className="settings-action" onClick={() => setManual(false)}>Done</button>
    </div>}
    <SelectionHelp harness={harness} model={model} levels={levels} catalog={catalog} loading={loading} error={error} discover={discover} />
  </div>;
}

function useDelegationModelCatalogs(config: DelegationPreferences | null, loadModels: (harness: string) => Promise<DelegationModelCatalog>) {
  const [catalogs, setCatalogs] = useState<Record<string, DelegationModelCatalog>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const discover = async (harness: string) => {
    if (!config || loading[harness]) return;
    setLoading(current => ({ ...current, [harness]: true }));
    setErrors(current => ({ ...current, [harness]: '' }));
    try {
      const result = await loadModels(harness);
      if (mounted.current) setCatalogs(current => ({ ...current, [harness]: result }));
    } catch (error) {
      if (mounted.current) setErrors(current => ({ ...current, [harness]: error instanceof Error ? error.message : String(error) }));
    } finally {
      if (mounted.current) setLoading(current => ({ ...current, [harness]: false }));
    }
  };
  return { catalogs, loading, errors, discover };
}

export function DelegationSettings({ policy, loadModels }: { policy: DelegationPreferencesPolicy; loadModels: (harness: string) => Promise<DelegationModelCatalog> }) {
  const { state, draft: config, setDraft, busy, dirty, error, changedElsewhere, persist, reload } = policy;
  const [screen, setScreen] = useState<'roles' | 'fallback'>('roles');
  const [roleID, setRoleID] = useState('');
  const [choiceID, setChoiceID] = useState('');
  const [iconsOpen, setIconsOpen] = useState(false);
  const [undo, setUndo] = useState<DelegationPreferences | null>(null);
  const [adoption, setAdoption] = useState<Record<string, string> | null>(null);
  const { catalogs, loading, errors: modelErrors, discover } = useDelegationModelCatalogs(config, loadModels);
  if (!config || !state) return <div role="status">{error || 'Loading delegation preferences…'}{error && <button className="settings-action" onClick={() => void reload()}>Retry</button>}</div>;
  const update = (next: DelegationPreferences) => setDraft(next);
  const role = config.roles.find(r => r.id === roleID);
  const expandedRole = role?.builtin
    ? state.expandedRoles.find(candidate => candidate.id === roleID && candidate.builtin === role.builtin) ?? role
    : role;
  const updateRole = (next: DelegationRole) => update({ ...config, roles: config.roles.map(r => r.id === next.id ? next : r) });
  const picker = (selection: DelegationSelection, onChange: (s: DelegationSelection) => void) => <SelectionPicker value={selection} onChange={onChange} harnesses={state.harnesses} catalog={catalogs[selection.harness]} loading={!!loading[selection.harness]} error={modelErrors[selection.harness]} discover={h => void discover(h)} />;
  const missingTemplates = state.templates.filter(template => !config.roles.some(role => role.builtin === template.builtin));
  const beginAdoption = () => setAdoption(Object.fromEntries(missingTemplates.map(template => [template.builtin ?? template.id, 'new'])));
  const addTemplates = () => {
    const replaced = new Set(Object.values(adoption ?? {}).filter(value => value !== 'new'));
    const additions = missingTemplates.map(template => {
      const key = template.builtin ?? template.id;
      const destination = adoption?.[key] ?? 'new';
      if (destination !== 'new') {
        const previous = config.roles.find(role => role.id === destination);
        if (previous) return { ...structuredClone(template), id: previous.id, enabled: previous.enabled, default_choice_id: previous.default_choice_id, choices: structuredClone(previous.choices) };
      }
      let roleID = template.id;
      while (config.roles.some(role => role.id === roleID)) roleID = id(template.id);
      return { ...structuredClone(template), id: roleID };
    });
    const next = { ...config, workflow_skill_enabled: true, roles: [...config.roles.filter(role => !replaced.has(role.id)), ...additions] };
    setAdoption(null);
    void persist(next, true);
  };
  const addRole = () => {
    const next: DelegationRole = { id: id('role'), name: 'New role', icon: '', enabled: true, description: '', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Default', when: '', selection: emptySelection() }] };
    update({ ...config, roles: [...config.roles, next] }); setRoleID(next.id); setChoiceID('default');
  };
  const removeRole = () => { setUndo(structuredClone(config)); update({ ...config, roles: config.roles.filter(r => r.id !== roleID) }); setRoleID(''); };
  const addChoice = () => {
    if (!role) return;
    const choice: DelegationChoice = { id: id('choice'), name: 'Alternative', when: '', selection: structuredClone(role.choices.find(c => c.id === role.default_choice_id)?.selection ?? emptySelection()) };
    updateRole({ ...role, choices: [...role.choices, choice] }); setChoiceID(choice.id);
  };
  return <div className="delegation-settings" data-testid="delegation-settings">
    <div className="delegation-enable">
      <div><h3>Use delegation preferences</h3></div>
      <label className="delegation-switch"><input type="checkbox" checked={config.enabled} disabled={busy} onChange={e => {
        const next = { ...config, enabled: e.target.checked };
        void persist(next);
      }} />{config.enabled ? 'On' : 'Off'}</label>
    </div>
    {error && <p role="alert" className="settings-warning">{error}</p>}
    {changedElsewhere && <p role="alert" className="settings-warning">Preferences changed elsewhere. Your draft is preserved. Revert changes before making a new edit.</p>}
    <fieldset disabled={busy} className="delegation-content">
      <div className="delegation-tabs settings-segmented" role="group" aria-label="Delegation settings">
        <button className={`settings-segmented-option ${screen === 'roles' ? 'active' : ''}`} aria-pressed={screen === 'roles'} onClick={() => { setScreen('roles'); setRoleID(''); }}>Roles</button>
        <button className={`settings-segmented-option ${screen === 'fallback' ? 'active' : ''}`} aria-pressed={screen === 'fallback'} onClick={() => { setScreen('fallback'); setRoleID(''); }}>Fallback</button>
      </div>
      {screen === 'fallback' ? <>
        <h3>When no role fits</h3>
        {picker(config.fallback.selection, selection => update({ ...config, fallback: { ...config.fallback, selection } }))}
        <label className="delegation-field">Instructions (optional)<textarea value={config.fallback.instructions} onChange={e => update({ ...config, fallback: { ...config.fallback, instructions: e.target.value } })} /></label>
      </> : role && expandedRole ? <>
        <div className="delegation-row between"><button className="settings-action" onClick={() => { setRoleID(''); setIconsOpen(false); }}>← All roles</button><button className="settings-action danger" onClick={removeRole}>Delete role</button></div>
        <div className="delegation-role-heading"><button className="delegation-icon" disabled={Boolean(role.builtin)} aria-label="Choose role icon" aria-expanded={iconsOpen} onClick={() => setIconsOpen(v => !v)}><DelegationRoleIcon icon={expandedRole.icon} name={expandedRole.name} /></button>
          <label className="delegation-field">Role name<input value={expandedRole.name} readOnly={Boolean(role.builtin)} onChange={e => updateRole({ ...role, name: e.target.value })} /></label></div>
        {!role.builtin && iconsOpen && <div className="delegation-icons" role="group" aria-label="Role icons"><button className="settings-action" onClick={() => { updateRole({ ...role, icon: '' }); setIconsOpen(false); }}>Initial</button>{DELEGATION_ICONS.map(icon => <button key={icon} className="delegation-icon" aria-label={`${icon} icon`} aria-pressed={role.icon === icon} onClick={() => { updateRole({ ...role, icon }); setIconsOpen(false); }}><DelegationRoleIcon icon={icon} name={role.name} /></button>)}</div>}
        <label className="delegation-field">When to choose this role<input value={expandedRole.description} readOnly={Boolean(role.builtin)} onChange={e => updateRole({ ...role, description: e.target.value })} /></label>
        <details className="delegation-behavior" open key={role.id}><summary>Instructions and stopping point</summary>
          <label className="delegation-field">Instructions<textarea value={expandedRole.instructions} readOnly={Boolean(role.builtin)} onChange={e => updateRole({ ...role, instructions: e.target.value })} /></label>
          <label className="delegation-field">Stopping point<textarea value={expandedRole.stopping_point} readOnly={Boolean(role.builtin)} onChange={e => updateRole({ ...role, stopping_point: e.target.value })} /></label>
        </details>
        {role.builtin && <button className="settings-action" onClick={() => {
          const copy = { ...structuredClone(expandedRole), id: id('role'), builtin: undefined, name: `${expandedRole.name} copy` };
          update({ ...config, roles: [...config.roles, copy] }); setRoleID(copy.id); setChoiceID(copy.default_choice_id);
        }}>Duplicate as custom role</button>}
        <div className="delegation-row between"><h3>Model choices</h3><button className="settings-action" data-testid="delegation-add-choice" onClick={addChoice}>+ Add alternative</button></div>
        {[...role.choices].sort((a, b) => Number(b.id === role.default_choice_id) - Number(a.id === role.default_choice_id)).map(choice => {
          const isDefault = role.default_choice_id === choice.id;
          const setChoice = (next: DelegationChoice) => updateRole({ ...role, choices: role.choices.map(c => c.id === next.id ? next : c) });
          return <section className="delegation-choice" key={choice.id}>
            <div className="delegation-row between"><div><h4>{choice.name}{isDefault && choice.name !== 'Default' && <span className="delegation-choice-default">Default</span>}</h4>{!isDefault && <p className="settings-hint">{choice.when || 'No condition set'}</p>}<p className="delegation-route">{route(choice.selection)}</p></div><button className="settings-action" aria-expanded={choiceID === choice.id} onClick={() => setChoiceID(choiceID === choice.id ? '' : choice.id)}>{choiceID === choice.id ? 'Close' : 'Edit'}</button></div>
            {choiceID === choice.id && <div className="delegation-choice-body">
              <label className="delegation-field">Choice name<input value={choice.name} onChange={e => setChoice({ ...choice, name: e.target.value })} /></label>
              {!isDefault && <label className="delegation-field">Use when<textarea value={choice.when} onChange={e => setChoice({ ...choice, when: e.target.value })} placeholder="For example: requirements are ambiguous or verification is difficult." /></label>}
              {picker(choice.selection, selection => setChoice({ ...choice, selection }))}
              <div className="delegation-row">
                {!isDefault && <button className="settings-action" onClick={() => { setUndo(structuredClone(config)); updateRole({ ...role, default_choice_id: choice.id }); }}>Make default</button>}
                <button className="settings-action" onClick={() => { const copy = { ...structuredClone(choice), id: id('choice'), name: `${choice.name} copy` }; updateRole({ ...role, choices: [...role.choices, copy] }); setChoiceID(copy.id); }}>Duplicate</button>
                {!isDefault && <button className="settings-action danger" onClick={() => { setUndo(structuredClone(config)); updateRole({ ...role, choices: role.choices.filter(c => c.id !== choice.id) }); }}>Remove alternative</button>}
              </div>
            </div>}
          </section>;
        })}
      </> : <>
        <div className="delegation-row between"><h3>Roles</h3><button className="settings-action" onClick={addRole}>+ New role</button></div>
        {config.roles.map(r => { const view = r.builtin ? state.expandedRoles.find(candidate => candidate.id === r.id && candidate.builtin === r.builtin) ?? r : r; return <div className={`delegation-role-row ${r.enabled ? '' : 'disabled'}`} key={r.id}><span className="delegation-icon"><DelegationRoleIcon icon={view.icon} name={view.name} /></span><div className="delegation-role-summary"><h4>{view.name}</h4><p className="settings-description">{view.description}</p><p className="delegation-route">{route(r.choices.find(c => c.id === r.default_choice_id)?.selection ?? emptySelection())}{r.choices.length > 1 ? ` · ${r.choices.length - 1} alternatives` : ''}</p></div><button className="settings-action" aria-label={`Edit ${view.name}`} onClick={() => { setRoleID(r.id); setChoiceID(r.default_choice_id); setIconsOpen(false); }}>Edit</button><input type="checkbox" aria-label={`Enable ${view.name}`} checked={r.enabled} onChange={e => updateRole({ ...r, enabled: e.target.checked })} /></div>; })}
        {adoption && <section className="delegation-choice"><h4>Adopt maintained roles</h4><p className="settings-description">Choose whether each role is added or replaces one custom row. Replacement keeps that row's enabled state and model choices.</p>
          {missingTemplates.map(template => { const key = template.builtin ?? template.id; return <label className="delegation-field" key={key}>{state.expandedRoles.find(role => role.builtin === template.builtin)?.name ?? key}<select value={adoption[key]} onChange={event => setAdoption({ ...adoption, [key]: event.target.value })}><option value="new">Add as a new role</option>{config.roles.filter(role => !role.builtin && !Object.values(adoption).includes(role.id)).map(role => <option key={role.id} value={role.id}>Replace {role.name}</option>)}{adoption[key] !== 'new' && <option value={adoption[key]}>Replace {config.roles.find(role => role.id === adoption[key])?.name}</option>}</select></label>; })}
          <div className="delegation-row"><button className="settings-action" onClick={() => setAdoption(null)}>Cancel</button><button className="settings-action primary" data-testid="delegation-add-attn-roles-confirm" onClick={addTemplates}>{dirty ? 'Save changes and add Attn roles' : 'Add Attn roles'}</button></div>
        </section>}
        {!adoption && <button className="settings-action" disabled={missingTemplates.length === 0 || changedElsewhere} onClick={beginAdoption}>{dirty ? 'Save changes and add Attn roles' : 'Add Attn roles'}</button>}
        <p className="settings-description">Adds Attn's maintained roles and installs the <code>attn-workflow</code> skill. Attn keeps their guidance updated; your model choices remain yours.</p>
      </>}
    </fieldset>
    {undo && <div role="status" className="delegation-row"><button className="settings-action" onClick={() => { update({ ...undo, revision: config.revision }); setUndo(null); }}>Undo</button></div>}
    <div className="delegation-save"><span role="status">{busy ? 'Saving…' : dirty ? 'Unsaved changes' : 'Saved'}</span><button className="settings-action" disabled={busy || (!dirty && !changedElsewhere)} onClick={() => { setUndo(null); void reload(true); }}>Revert changes</button><button className="settings-action primary" data-testid="delegation-save" disabled={busy || !dirty || changedElsewhere} onClick={() => void persist(config)}>Save</button></div>
  </div>;
}
