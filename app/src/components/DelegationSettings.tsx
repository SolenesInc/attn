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

function adoptMaintainedRoles(
  config: DelegationPreferences,
  templates: DelegationRole[],
  adoption: Record<string, string>,
): DelegationPreferences {
  const replaced = new Set<string>();
  for (const destination of Object.values(adoption)) {
    if (destination !== 'new') replaced.add(destination);
  }
  const roleByID = new Map(config.roles.map((role) => [role.id, role]));
  const occupiedIDs = new Set(config.roles.map((role) => role.id));
  const additions: DelegationRole[] = [];
  for (const template of templates) {
    const key = template.builtin ?? template.id;
    const destination = adoption[key] ?? 'new';
    const previous = destination === 'new' ? undefined : roleByID.get(destination);
    if (previous) {
      additions.push({
        ...structuredClone(template),
        id: previous.id,
        enabled: previous.enabled,
        default_choice_id: previous.default_choice_id,
        choices: structuredClone(previous.choices),
      });
      continue;
    }
    let roleID = template.id;
    while (occupiedIDs.has(roleID)) roleID = id(template.id);
    occupiedIDs.add(roleID);
    additions.push({ ...structuredClone(template), id: roleID });
  }
  const roles = config.roles.filter((role) => !replaced.has(role.id));
  roles.push(...additions);
  return { ...config, workflow_skill_enabled: true, roles };
}

function RoleRows({ roles, expandedRoles, onEdit, onToggle }: {
  roles: DelegationRole[];
  expandedRoles: DelegationRole[];
  onEdit: (role: DelegationRole) => void;
  onToggle: (role: DelegationRole, enabled: boolean) => void;
}) {
  const views = new Map(expandedRoles.map((role) => [role.id, role]));
  return roles.map((role) => {
    const view = role.builtin ? views.get(role.id) ?? role : role;
    const defaultChoice = role.choices.find((choice) => choice.id === role.default_choice_id);
    return <div className={`delegation-role-row ${role.enabled ? '' : 'disabled'}`} key={role.id}>
      <span className="delegation-icon"><DelegationRoleIcon icon={view.icon} name={view.name} /></span>
      <div className="delegation-role-summary">
        <h4>{view.name}</h4>
        <p className="settings-description">{view.description}</p>
        <p className="delegation-route">{route(defaultChoice?.selection ?? emptySelection())}{role.choices.length > 1 ? ` · ${role.choices.length - 1} alternatives` : ''}</p>
      </div>
      <button className="settings-action" aria-label={`Edit ${view.name}`} onClick={() => onEdit(role)}>Edit</button>
      <input type="checkbox" aria-label={`Enable ${view.name}`} checked={role.enabled} onChange={(event) => onToggle(role, event.target.checked)} />
    </div>;
  });
}

function AdoptionPanel({ config, expandedRoles, templates, adoption, dirty, onChange, onCancel, onConfirm }: {
  config: DelegationPreferences;
  expandedRoles: DelegationRole[];
  templates: DelegationRole[];
  adoption: Record<string, string>;
  dirty: boolean;
  onChange: (next: Record<string, string>) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const roleNames = new Map(config.roles.map((role) => [role.id, role.name]));
  const builtinNames = new Map(expandedRoles.map((role) => [role.builtin, role.name]));
  const selected = new Set(Object.values(adoption));
  const options = new Map<string, DelegationRole[]>();
  for (const template of templates) {
    const key = template.builtin ?? template.id;
    const available: DelegationRole[] = [];
    for (const role of config.roles) {
      if (!role.builtin && (!selected.has(role.id) || adoption[key] === role.id)) available.push(role);
    }
    options.set(key, available);
  }
  return <section className="delegation-choice">
    <h4>Adopt maintained roles</h4>
    <p className="settings-description">Choose whether each role is added or replaces one custom row. Replacement keeps that row's enabled state and model choices.</p>
    {templates.map((template) => {
      const key = template.builtin ?? template.id;
      return <label className="delegation-field" key={key}>{builtinNames.get(template.builtin) ?? key}
        <select value={adoption[key]} onChange={(event) => onChange({ ...adoption, [key]: event.target.value })}>
          <option value="new">Add as a new role</option>
          {options.get(key)?.map((role) => <option key={role.id} value={role.id}>Replace {role.name}</option>)}
          {adoption[key] !== 'new' && !options.get(key)?.some((role) => role.id === adoption[key]) && <option value={adoption[key]}>Replace {roleNames.get(adoption[key])}</option>}
        </select>
      </label>;
    })}
    <div className="delegation-row">
      <button className="settings-action" onClick={onCancel}>Cancel</button>
      <button className="settings-action primary" data-testid="delegation-add-attn-roles-confirm" onClick={onConfirm}>{dirty ? 'Save changes and add Attn roles' : 'Add Attn roles'}</button>
    </div>
  </section>;
}

type SelectionRenderer = (selection: DelegationSelection, onChange: (selection: DelegationSelection) => void) => React.ReactNode;

function RoleEditor({ config, role, view, choiceID, iconsOpen, picker, onUpdate, onBack, onDelete, onChoice, onIcons, onUndo, onDuplicate }: {
  config: DelegationPreferences;
  role: DelegationRole;
  view: DelegationRole;
  choiceID: string;
  iconsOpen: boolean;
  picker: SelectionRenderer;
  onUpdate: (role: DelegationRole) => void;
  onBack: () => void;
  onDelete: () => void;
  onChoice: (id: string) => void;
  onIcons: (open: boolean) => void;
  onUndo: (config: DelegationPreferences) => void;
  onDuplicate: (role: DelegationRole) => void;
}) {
  const addChoice = () => {
    const selection = role.choices.find((choice) => choice.id === role.default_choice_id)?.selection ?? emptySelection();
    const choice: DelegationChoice = { id: id('choice'), name: 'Alternative', when: '', selection: structuredClone(selection) };
    onUpdate({ ...role, choices: [...role.choices, choice] });
    onChoice(choice.id);
  };
  const duplicate = () => {
    const copy = { ...structuredClone(view), id: id('role'), builtin: undefined, name: `${view.name} copy` };
    return copy;
  };
  return <>
    <div className="delegation-row between"><button className="settings-action" onClick={onBack}>← All roles</button><button className="settings-action danger" onClick={onDelete}>Delete role</button></div>
    <div className="delegation-role-heading"><button className="delegation-icon" disabled={Boolean(role.builtin)} aria-label="Choose role icon" aria-expanded={iconsOpen} onClick={() => onIcons(!iconsOpen)}><DelegationRoleIcon icon={view.icon} name={view.name} /></button>
      <label className="delegation-field">Role name<input value={view.name} readOnly={Boolean(role.builtin)} onChange={(event) => onUpdate({ ...role, name: event.target.value })} /></label></div>
    {!role.builtin && iconsOpen && <div className="delegation-icons" role="group" aria-label="Role icons"><button className="settings-action" onClick={() => { onUpdate({ ...role, icon: '' }); onIcons(false); }}>Initial</button>{DELEGATION_ICONS.map((icon) => <button key={icon} className="delegation-icon" aria-label={`${icon} icon`} aria-pressed={role.icon === icon} onClick={() => { onUpdate({ ...role, icon }); onIcons(false); }}><DelegationRoleIcon icon={icon} name={role.name} /></button>)}</div>}
    <label className="delegation-field">When to choose this role<input value={view.description} readOnly={Boolean(role.builtin)} onChange={(event) => onUpdate({ ...role, description: event.target.value })} /></label>
    <details className="delegation-behavior" open key={role.id}><summary>Instructions and stopping point</summary>
      <label className="delegation-field">Instructions<textarea value={view.instructions} readOnly={Boolean(role.builtin)} onChange={(event) => onUpdate({ ...role, instructions: event.target.value })} /></label>
      <label className="delegation-field">Stopping point<textarea value={view.stopping_point} readOnly={Boolean(role.builtin)} onChange={(event) => onUpdate({ ...role, stopping_point: event.target.value })} /></label>
    </details>
    {role.builtin && <button className="settings-action" onClick={() => onDuplicate(duplicate())}>Duplicate as custom role</button>}
    <div className="delegation-row between"><h3>Model choices</h3><button className="settings-action" data-testid="delegation-add-choice" onClick={addChoice}>+ Add alternative</button></div>
    {[...role.choices].sort((a, b) => Number(b.id === role.default_choice_id) - Number(a.id === role.default_choice_id)).map((choice) => {
      const isDefault = role.default_choice_id === choice.id;
      const setChoice = (next: DelegationChoice) => onUpdate({ ...role, choices: role.choices.map((candidate) => candidate.id === next.id ? next : candidate) });
      return <section className="delegation-choice" key={choice.id}>
        <div className="delegation-row between"><div><h4>{choice.name}{isDefault && choice.name !== 'Default' && <span className="delegation-choice-default">Default</span>}</h4>{!isDefault && <p className="settings-hint">{choice.when || 'No condition set'}</p>}<p className="delegation-route">{route(choice.selection)}</p></div><button className="settings-action" aria-expanded={choiceID === choice.id} onClick={() => onChoice(choiceID === choice.id ? '' : choice.id)}>{choiceID === choice.id ? 'Close' : 'Edit'}</button></div>
        {choiceID === choice.id && <div className="delegation-choice-body">
          <label className="delegation-field">Choice name<input value={choice.name} onChange={(event) => setChoice({ ...choice, name: event.target.value })} /></label>
          {!isDefault && <label className="delegation-field">Use when<textarea value={choice.when} onChange={(event) => setChoice({ ...choice, when: event.target.value })} placeholder="For example: requirements are ambiguous or verification is difficult." /></label>}
          {picker(choice.selection, (selection) => setChoice({ ...choice, selection }))}
          <div className="delegation-row">
            {!isDefault && <button className="settings-action" onClick={() => { onUndo(structuredClone(config)); onUpdate({ ...role, default_choice_id: choice.id }); }}>Make default</button>}
            <button className="settings-action" onClick={() => { const copy = { ...structuredClone(choice), id: id('choice'), name: `${choice.name} copy` }; onUpdate({ ...role, choices: [...role.choices, copy] }); onChoice(copy.id); }}>Duplicate</button>
            {!isDefault && <button className="settings-action danger" onClick={() => { onUndo(structuredClone(config)); onUpdate({ ...role, choices: role.choices.filter((candidate) => candidate.id !== choice.id) }); }}>Remove alternative</button>}
          </div>
        </div>}
      </section>;
    })}
  </>;
}

function FallbackEditor({ config, picker, onUpdate }: {
  config: DelegationPreferences;
  picker: SelectionRenderer;
  onUpdate: (next: DelegationPreferences) => void;
}) {
  return <>
    <h3>When no role fits</h3>
    {picker(config.fallback.selection, (selection) => onUpdate({ ...config, fallback: { ...config.fallback, selection } }))}
    <label className="delegation-field">Instructions (optional)<textarea value={config.fallback.instructions} onChange={(event) => onUpdate({ ...config, fallback: { ...config.fallback, instructions: event.target.value } })} /></label>
  </>;
}

function RolesOverview({ config, expandedRoles, templates, adoption, dirty, changedElsewhere, onAddRole, onEdit, onToggle, onAdoption, onConfirm }: {
  config: DelegationPreferences;
  expandedRoles: DelegationRole[];
  templates: DelegationRole[];
  adoption: Record<string, string> | null;
  dirty: boolean;
  changedElsewhere: boolean;
  onAddRole: () => void;
  onEdit: (role: DelegationRole) => void;
  onToggle: (role: DelegationRole, enabled: boolean) => void;
  onAdoption: (next: Record<string, string> | null) => void;
  onConfirm: () => void;
}) {
  const begin = () => onAdoption(Object.fromEntries(templates.map((template) => [template.builtin ?? template.id, 'new'])));
  return <>
    <div className="delegation-row between"><h3>Roles</h3><button className="settings-action" onClick={onAddRole}>+ New role</button></div>
    <RoleRows roles={config.roles} expandedRoles={expandedRoles} onEdit={onEdit} onToggle={onToggle} />
    {adoption && <AdoptionPanel config={config} expandedRoles={expandedRoles} templates={templates} adoption={adoption} dirty={dirty} onChange={onAdoption} onCancel={() => onAdoption(null)} onConfirm={onConfirm} />}
    {!adoption && <button className="settings-action" disabled={templates.length === 0 || changedElsewhere} onClick={begin}>{dirty ? 'Save changes and add Attn roles' : 'Add Attn roles'}</button>}
    <p className="settings-description">Adds Attn's maintained roles and installs the <code>attn-workflow</code> skill. Attn keeps their guidance updated; your model choices remain yours.</p>
  </>;
}

function SettingsFooter({ busy, dirty, changedElsewhere, onRevert, onSave }: {
  busy: boolean;
  dirty: boolean;
  changedElsewhere: boolean;
  onRevert: () => void;
  onSave: () => void;
}) {
  return <div className="delegation-save">
    <span role="status">{busy ? 'Saving…' : dirty ? 'Unsaved changes' : 'Saved'}</span>
    <button className="settings-action" disabled={busy || (!dirty && !changedElsewhere)} onClick={onRevert}>Revert changes</button>
    <button className="settings-action primary" data-testid="delegation-save" disabled={busy || !dirty || changedElsewhere} onClick={onSave}>Save</button>
  </div>;
}

function SettingsWarnings({ error, changedElsewhere }: { error: string; changedElsewhere: boolean }) {
  return <>
    {error && <p role="alert" className="settings-warning">{error}</p>}
    {changedElsewhere && <p role="alert" className="settings-warning">Preferences changed elsewhere. Your draft is preserved. Revert changes before making a new edit.</p>}
  </>;
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
  const addTemplates = () => {
    const next = adoptMaintainedRoles(config, missingTemplates, adoption ?? {});
    setAdoption(null);
    void persist(next, true);
  };
  const addRole = () => {
    const next: DelegationRole = { id: id('role'), name: 'New role', icon: '', enabled: true, description: '', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Default', when: '', selection: emptySelection() }] };
    update({ ...config, roles: [...config.roles, next] }); setRoleID(next.id); setChoiceID('default');
  };
  const removeRole = () => { setUndo(structuredClone(config)); update({ ...config, roles: config.roles.filter(r => r.id !== roleID) }); setRoleID(''); };
  return <div className="delegation-settings" data-testid="delegation-settings">
    <div className="delegation-enable">
      <div><h3>Use delegation preferences</h3></div>
      <label className="delegation-switch"><input type="checkbox" checked={config.enabled} disabled={busy} onChange={e => {
        const next = { ...config, enabled: e.target.checked };
        void persist(next);
      }} />{config.enabled ? 'On' : 'Off'}</label>
    </div>
    <SettingsWarnings error={error} changedElsewhere={changedElsewhere} />
    <fieldset disabled={busy} className="delegation-content">
      <div className="delegation-tabs settings-segmented" role="group" aria-label="Delegation settings">
        <button className={`settings-segmented-option ${screen === 'roles' ? 'active' : ''}`} aria-pressed={screen === 'roles'} onClick={() => { setScreen('roles'); setRoleID(''); }}>Roles</button>
        <button className={`settings-segmented-option ${screen === 'fallback' ? 'active' : ''}`} aria-pressed={screen === 'fallback'} onClick={() => { setScreen('fallback'); setRoleID(''); }}>Fallback</button>
      </div>
      {screen === 'fallback'
        ? <FallbackEditor config={config} picker={picker} onUpdate={update} />
        : role && expandedRole
          ? <RoleEditor config={config} role={role} view={expandedRole} choiceID={choiceID} iconsOpen={iconsOpen} picker={picker} onUpdate={updateRole} onBack={() => { setRoleID(''); setIconsOpen(false); }} onDelete={removeRole} onChoice={setChoiceID} onIcons={setIconsOpen} onUndo={setUndo} onDuplicate={(copy) => { update({ ...config, roles: [...config.roles, copy] }); setRoleID(copy.id); setChoiceID(copy.default_choice_id); }} />
          : <RolesOverview config={config} expandedRoles={state.expandedRoles} templates={missingTemplates} adoption={adoption} dirty={dirty} changedElsewhere={changedElsewhere} onAddRole={addRole} onEdit={(next) => { setRoleID(next.id); setChoiceID(next.default_choice_id); setIconsOpen(false); }} onToggle={(next, enabled) => updateRole({ ...next, enabled })} onAdoption={setAdoption} onConfirm={addTemplates} />}
    </fieldset>
    {undo && <div role="status" className="delegation-row"><button className="settings-action" onClick={() => { update({ ...undo, revision: config.revision }); setUndo(null); }}>Undo</button></div>}
    <SettingsFooter busy={busy} dirty={dirty} changedElsewhere={changedElsewhere} onRevert={() => { setUndo(null); void reload(true); }} onSave={() => void persist(config)} />
  </div>;
}
