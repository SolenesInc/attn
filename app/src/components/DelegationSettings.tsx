import { useEffect, useState } from 'react';
import type { DelegationChoice, DelegationPreferences, DelegationRole, DelegationSelection, DelegationHarness } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { DelegationPreferencesPolicy } from '../hooks/useDelegationPreferences';
import { DelegationRoleIcon } from './DelegationRoleIcon';
import { DelegationModelPopover, knownModelName, type Anchor } from './DelegationModelPopover';
import './DelegationSettings.css';

const emptySelection = (): DelegationSelection => ({ harness: '', provider: '', model: '', effort: '' });
const id = (prefix: string) => `${prefix}-${crypto.randomUUID()}`;
const roleViewKey = (role: Pick<DelegationRole, 'id' | 'builtin'>) => `${role.id}:${role.builtin ?? ''}`;
const firstLine = (text: string) => text.split('\n').find(line => line.trim())?.trim() ?? '';
const complete = (selection: DelegationSelection) => selection.harness !== '';
const defaultChoice = (role: DelegationRole) => role.choices.find(choice => choice.id === role.default_choice_id) ?? role.choices[0];
const alternatives = (role: DelegationRole) => role.choices.filter(choice => choice.id !== role.default_choice_id);
const DELEGATION_ICONS = ['search', 'diamond', 'code', 'arrow', 'list', 'bug', 'spark', 'circle'] as const;
const FALLBACK = '__fallback';

export const delegationLiveCount = (preferences: DelegationPreferences | null) => preferences?.enabled ? preferences.roles.filter(role => role.enabled && complete(defaultChoice(role).selection)).length : 0;

export function DelegationSwitch({ policy }: { policy: DelegationPreferencesPolicy }) {
  const { preferences, save } = policy;
  if (!preferences) return null;
  return <button type="button" role="switch" aria-checked={preferences.enabled} aria-label="Delegation preferences" className="delegation-switch" onClick={() => void save({ ...preferences, enabled: !preferences.enabled })}>
    <span className="delegation-switch-track" /><span>{preferences.enabled ? 'On' : 'Off'}</span>
  </button>;
}

type PopoverTarget = { key: string; anchor: Anchor };
type Undo = { label: string; previous: DelegationPreferences };

function adoptMaintainedRoles(config: DelegationPreferences, templates: DelegationRole[], adoption: Record<string, string>): DelegationPreferences {
  const replaced = new Set(Object.values(adoption).filter(destination => destination !== 'new'));
  const roleByID = new Map(config.roles.map(role => [role.id, role]));
  const occupied = new Set(config.roles.map(role => role.id));
  const additions: DelegationRole[] = [];
  for (const template of templates) {
    const destination = adoption[template.builtin ?? template.id] ?? 'new';
    const previous = destination === 'new' ? undefined : roleByID.get(destination);
    if (previous) {
      additions.push({ ...structuredClone(template), id: previous.id, enabled: previous.enabled, default_choice_id: previous.default_choice_id, choices: structuredClone(previous.choices) });
      continue;
    }
    let roleID = template.id;
    while (occupied.has(roleID)) roleID = id(template.id);
    occupied.add(roleID);
    additions.push({ ...structuredClone(template), id: roleID });
  }
  return { ...config, workflow_skill_enabled: true, roles: [...config.roles.filter(role => !replaced.has(role.id)), ...additions] };
}

function ModelCell({ selection, harnesses, label, open, onOpen }: { selection: DelegationSelection; harnesses: DelegationHarness[]; label: string; open: boolean; onOpen: (anchor: Anchor) => void }) {
  const harness = harnesses.find(h => h.id === selection.harness);
  let className = 'delegation-model';
  let body: React.ReactNode;
  if (!complete(selection)) { className += ' unset'; body = 'Choose a model'; }
  else if (harness && !harness.model_pin) { className += ' pinned'; body = <><span className="h">{harness.name}</span><span className="m">its own model</span></>; }
  else body = <><span className="h">{harness?.name ?? selection.harness}</span><span className="m">{selection.model ? knownModelName(selection.harness, selection.provider, selection.model) || `${selection.provider ? `${selection.provider}/` : ''}${selection.model}` : 'default'}</span>{selection.effort && <span className="e">{selection.effort}</span>}</>;
  return <button type="button" className={className} aria-label={label} aria-haspopup="dialog" aria-expanded={open} onClick={e => { const r = e.currentTarget.getBoundingClientRect(); onOpen({ top: r.top, bottom: r.bottom, left: r.left, right: r.right }); }}>
    {body}<span className="delegation-caret" aria-hidden="true">⌄</span>
  </button>;
}

function TextField({ id: fieldID, label, value, onCommit, placeholder, hint, multiline = true, note }: { id: string; label: string; value: string; onCommit: (value: string) => void; placeholder?: string; hint?: string; multiline?: boolean; note?: string }) {
  const commit = (next: string) => { if (next !== value) onCommit(next); };
  return <div className="delegation-field">
    <label htmlFor={fieldID}>{label}{note && <span className="delegation-field-note">{note}</span>}</label>
    {multiline
      ? <textarea id={fieldID} key={value} defaultValue={value} placeholder={placeholder} onBlur={e => commit(e.target.value)} />
      : <input id={fieldID} key={value} defaultValue={value} placeholder={placeholder} onBlur={e => commit(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') e.currentTarget.blur(); }} />}
    {hint && <span className="delegation-hint">{hint}</span>}
  </div>;
}

function ReadOnlyField({ label, value, note }: { label: string; value: string; note?: string }) {
  return <div className="delegation-field"><span className="delegation-field-label">{label}{note && <span className="delegation-field-note">{note}</span>}</span><div className="delegation-ro">{value}</div></div>;
}

function AdoptionPanel({ config, templates, names, adoption, onChange, onCancel, onConfirm }: {
  config: DelegationPreferences;
  templates: DelegationRole[];
  names: (role: DelegationRole) => string;
  adoption: Record<string, string>;
  onChange: (next: Record<string, string>) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const taken = new Set(Object.values(adoption));
  return <section className="delegation-confirm" aria-label="Adopt maintained roles">
    <h4>Some Attn roles look like roles you already have</h4>
    <p>Choose whether to add them beside your custom rows or replace one. Replacing keeps that row's model choices and on/off state.</p>
    {templates.map(template => {
      const key = template.builtin ?? template.id;
      const options = config.roles.filter(role => !role.builtin && (!taken.has(role.id) || adoption[key] === role.id));
      return <label className="delegation-confirm-line" key={key}><strong>{names(template)}</strong>
        <select value={adoption[key]} onChange={event => onChange({ ...adoption, [key]: event.target.value })}>
          <option value="new">Add as a new role</option>
          {options.map(role => <option key={role.id} value={role.id}>Replace {role.name}</option>)}
        </select>
      </label>;
    })}
    <div className="delegation-actions">
      <button type="button" className="settings-action" onClick={onCancel}>Cancel</button>
      <button type="button" className="settings-action primary" data-testid="delegation-add-attn-roles-confirm" onClick={onConfirm}>Add Attn roles</button>
    </div>
  </section>;
}

export function DelegationSettings({ policy, loadModels }: { policy: DelegationPreferencesPolicy; loadModels: (harness: string) => Promise<DelegationModelCatalog> }) {
  const { state, preferences: config, error, loaded, reload, save } = policy;
  const [expanded, setExpanded] = useState<string | null>(null);
  const [expandedAlt, setExpandedAlt] = useState<string | null>(null);
  const [popover, setPopover] = useState<PopoverTarget | null>(null);
  const [undo, setUndo] = useState<Undo | null>(null);
  const [adoption, setAdoption] = useState<Record<string, string> | null>(null);
  useEffect(() => { setUndo(null); }, [loaded]);
  if (!config || !state) return <div role="status">{error || 'Loading delegation preferences…'}{error && <button type="button" className="settings-action" onClick={() => void reload()}>Retry</button>}</div>;

  const views = new Map(state.expandedRoles.map(role => [roleViewKey(role), role]));
  const view = (role: DelegationRole) => role.builtin ? views.get(roleViewKey(role)) ?? role : role;
  const commit = (next: DelegationPreferences) => { setUndo(null); void save(next); };
  const commitUndoable = (next: DelegationPreferences, label: string) => { void save(next); setUndo({ label, previous: structuredClone(config) }); };
  const updateRole = (next: DelegationRole) => commit({ ...config, roles: config.roles.map(role => role.id === next.id ? next : role) });
  const updateChoice = (role: DelegationRole, next: DelegationChoice) => updateRole({ ...role, choices: role.choices.map(choice => choice.id === next.id ? next : choice) });
  const toggle = (key: string) => { setExpanded(current => current === key ? null : key); setExpandedAlt(null); };

  const selectionAt = (key: string): { value: DelegationSelection; set: (s: DelegationSelection) => void } | null => {
    if (key === FALLBACK) return { value: config.fallback.selection, set: selection => commit({ ...config, fallback: { ...config.fallback, selection } }) };
    const [roleID, choiceID] = key.split('/');
    const role = config.roles.find(candidate => candidate.id === roleID);
    const choice = role?.choices.find(candidate => candidate.id === choiceID);
    if (!role || !choice) return null;
    return { value: choice.selection, set: selection => updateChoice(role, { ...choice, selection }) };
  };
  const openPopover = (key: string, anchor: Anchor) => setPopover(current => current?.key === key ? null : { key, anchor });

  const addRole = () => {
    const role: DelegationRole = { id: id('role'), name: 'New role', icon: '', enabled: true, description: '', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Default', when: '', selection: emptySelection() }] };
    commit({ ...config, roles: [...config.roles, role] });
    setExpanded(role.id);
    setExpandedAlt(null);
  };
  const missing = state.templates.filter(template => !config.roles.some(role => role.builtin === template.builtin));
  const templateName = (template: DelegationRole) => view(template).name || template.builtin || template.id;
  const adopt = () => {
    const conflicts = missing.filter(template => config.roles.some(role => !role.builtin && role.name.trim().toLowerCase().startsWith(templateName(template).toLowerCase())));
    if (conflicts.length) { setAdoption(Object.fromEntries(missing.map(template => [template.builtin ?? template.id, 'new']))); return; }
    setUndo(null);
    void save(adoptMaintainedRoles(config, missing, {}), true);
  };
  const confirmAdoption = () => { setUndo(null); void save(adoptMaintainedRoles(config, missing, adoption ?? {}), true); setAdoption(null); };

  const renderRole = (role: DelegationRole) => {
    const v = view(role);
    const open = expanded === role.id;
    const main = defaultChoice(role);
    const alts = alternatives(role);
    const needs = role.enabled && !complete(main.selection);
    const readOnly = Boolean(role.builtin);
    const cellKey = `${role.id}/${main.id}`;
    return <div className={`delegation-row ${role.enabled ? '' : 'off'} ${open ? 'open' : ''}`} key={role.id} data-role-id={role.id}>
      <div className="delegation-row-main">
        <span className="delegation-icon"><DelegationRoleIcon icon={v.icon} name={v.name} /></span>
        <button type="button" className="delegation-who" aria-label={v.name} aria-expanded={open} onClick={() => toggle(role.id)}>
          <span className="delegation-name">{v.name}{readOnly && <span className="delegation-tag">Attn</span>}{!role.enabled && <span className="delegation-tag">Off</span>}{needs && <span className="delegation-tag needs">Needs a model</span>}</span>
          <span className="delegation-desc">{firstLine(v.description)}</span>
        </button>
        <span className="delegation-cell">
          <ModelCell selection={main.selection} harnesses={state.harnesses} label={`Model for ${v.name}`} open={popover?.key === cellKey} onOpen={anchor => openPopover(cellKey, anchor)} />
          {alts.length > 0 && !open && <button type="button" className="delegation-altchip" onClick={() => setExpanded(role.id)}>+{alts.length} alternative{alts.length === 1 ? '' : 's'}</button>}
        </span>
        <button type="button" className={`delegation-more ${open ? 'open' : ''}`} aria-label={`${v.name} details`} aria-expanded={open} onClick={() => toggle(role.id)}>···</button>
      </div>
      {open && <div className="delegation-details">
        {readOnly
          ? <>
            <ReadOnlyField label="Instructions" value={v.instructions} note="maintained by Attn, updates with releases" />
            <ReadOnlyField label="Stops when" value={v.stopping_point} />
          </>
          : <>
            <TextField id={`name-${role.id}`} label="Name" value={role.name} multiline={false} onCommit={name => { if (name.trim()) updateRole({ ...role, name: name.trim() }); }} />
            <div className="delegation-icons" role="group" aria-label="Role icon">
              <button type="button" className={`delegation-icon pick ${role.icon === '' ? 'active' : ''}`} aria-label="Initial as icon" aria-pressed={role.icon === ''} onClick={() => updateRole({ ...role, icon: '' })}><DelegationRoleIcon icon="" name={role.name} /></button>
              {DELEGATION_ICONS.map(icon => <button key={icon} type="button" className={`delegation-icon pick ${role.icon === icon ? 'active' : ''}`} aria-label={`${icon} icon`} aria-pressed={role.icon === icon} onClick={() => updateRole({ ...role, icon })}><DelegationRoleIcon icon={icon} name={role.name} /></button>)}
            </div>
            <TextField id={`desc-${role.id}`} label="When to choose this role" value={role.description} placeholder="Describe the work this role fits. The agent reads this to pick a role, so say what it looks like and what it is not for." hint="Prose, as long as it needs to be. The first line is what the table shows." onCommit={description => updateRole({ ...role, description })} />
            <TextField id={`ins-${role.id}`} label="Instructions" value={role.instructions} onCommit={instructions => updateRole({ ...role, instructions })} />
            <TextField id={`stop-${role.id}`} label="Stops when" value={role.stopping_point} onCommit={stopping_point => updateRole({ ...role, stopping_point })} />
          </>}
      </div>}
      {open && alts.map(alt => {
        const altOpen = expandedAlt === alt.id;
        const when = firstLine(alt.when);
        const altKey = `${role.id}/${alt.id}`;
        return <div key={alt.id}>
          <div className="delegation-alt">
            <span />
            <button type="button" className="delegation-alt-who" aria-label={alt.name || 'Unnamed alternative'} aria-expanded={altOpen} onClick={() => setExpandedAlt(altOpen ? null : alt.id)}>
              <span className="delegation-alt-name">{alt.name || 'Unnamed alternative'}</span>
              <span className={`delegation-alt-when ${when ? '' : 'blank'}`}><span className="w">when</span>{when || 'No condition yet. The agent cannot pick this.'}</span>
            </button>
            <ModelCell selection={alt.selection} harnesses={state.harnesses} label={`Model for ${alt.name || 'alternative'}`} open={popover?.key === altKey} onOpen={anchor => openPopover(altKey, anchor)} />
            <button type="button" className="delegation-rm" aria-label={`Remove ${alt.name || 'alternative'}`} onClick={() => { commitUndoable({ ...config, roles: config.roles.map(r => r.id === role.id ? { ...role, choices: role.choices.filter(c => c.id !== alt.id) } : r) }, `Removed ${alt.name || 'alternative'} from ${v.name}`); if (altOpen) setExpandedAlt(null); }} title={`Remove ${alt.name || 'alternative'}`}>×</button>
          </div>
          {altOpen && <div className="delegation-alt-edit">
            <TextField id={`altname-${alt.id}`} label="Name" value={alt.name} multiline={false} placeholder="Short label, shown to the agent with the condition" onCommit={name => updateChoice(role, { ...alt, name: name.trim() || 'Alternative' })} />
            <TextField id={`when-${alt.id}`} label="When to use this instead of the default" value={alt.when} placeholder="Describe the work this alternative fits. The agent reads this to decide, so say what it looks like and what it is not for." hint="Prose, as long as it needs to be. The first line is what the table shows." onCommit={when => updateChoice(role, { ...alt, when })} />
          </div>}
        </div>;
      })}
      {open && <div className="delegation-alt add"><span /><button type="button" className="settings-action quiet" onClick={() => {
        const choice: DelegationChoice = { id: id('choice'), name: 'Alternative', when: '', selection: structuredClone(main.selection) };
        updateRole({ ...role, choices: [...role.choices, choice] });
        setExpandedAlt(choice.id);
      }}>+ Alternative model</button></div>}
      {open && <div className="delegation-details actions"><div className="delegation-actions">
        {readOnly && <button type="button" className="settings-action" onClick={() => {
          const copy: DelegationRole = { ...structuredClone(v), id: id('role'), builtin: undefined, name: `${v.name} (custom)` };
          const index = config.roles.findIndex(r => r.id === role.id);
          commit({ ...config, roles: [...config.roles.slice(0, index + 1), copy, ...config.roles.slice(index + 1)] });
          setExpanded(copy.id);
        }}>Make an editable copy</button>}
        <span className="delegation-spacer" />
        <button type="button" className="settings-action quiet" onClick={() => updateRole({ ...role, enabled: !role.enabled })}>{role.enabled ? 'Turn off' : 'Turn on'}</button>
        <button type="button" className="settings-action quiet danger" onClick={() => { commitUndoable({ ...config, roles: config.roles.filter(r => r.id !== role.id) }, `Deleted ${v.name}`); setExpanded(null); }}>Delete</button>
      </div></div>}
    </div>;
  };

  const renderFallback = () => {
    const open = expanded === FALLBACK;
    return <div className={`delegation-row ${open ? 'open' : ''}`} data-role-id="fallback">
      <div className="delegation-row-main">
        <span className="delegation-icon fallback"><DelegationRoleIcon icon="arrow" name="Anything else" /></span>
        <button type="button" className="delegation-who" aria-label="Anything else" aria-expanded={open} onClick={() => toggle(FALLBACK)}>
          <span className="delegation-name">Anything else</span>
          <span className="delegation-desc">When no role fits the work. Agents use this instead of choosing a model themselves.</span>
        </button>
        <span className="delegation-cell"><ModelCell selection={config.fallback.selection} harnesses={state.harnesses} label="Model for anything else" open={popover?.key === FALLBACK} onOpen={anchor => openPopover(FALLBACK, anchor)} /></span>
        <button type="button" className={`delegation-more ${open ? 'open' : ''}`} aria-label="Anything else details" aria-expanded={open} onClick={() => toggle(FALLBACK)}>···</button>
      </div>
      {open && <div className="delegation-details">
        <TextField id="fb-ins" label="Instructions (optional)" value={config.fallback.instructions} placeholder="Anything every unmatched delegation should hear." onCommit={instructions => commit({ ...config, fallback: { ...config.fallback, instructions } })} />
      </div>}
    </div>;
  };

  const live = config.roles.filter(role => role.enabled && complete(defaultChoice(role).selection)).length;
  const needs = config.roles.filter(role => role.enabled && !complete(defaultChoice(role).selection)).length;
  const off = config.roles.filter(role => !role.enabled).length;
  const fallbackReady = complete(config.fallback.selection);
  const liveText = !config.enabled
    ? 'Off. Agents choose harness and model themselves. Your table is kept.'
    : `Agents see ${live} of ${config.roles.length} role${config.roles.length === 1 ? '' : 's'}${fallbackReady ? ' and the fallback' : ''}.${needs ? ` ${needs} need${needs === 1 ? 's' : ''} a model.` : ''}${off ? ` ${off} off.` : ''}`;
  const target = popover ? selectionAt(popover.key) : null;

  return <div className="delegation-settings" data-testid="delegation-settings">
    <p className={`delegation-lead ${config.enabled ? '' : 'off'}`}>{config.enabled
      ? 'Roles guide agents that delegate. Each role has a model; an agent reads this table and picks the row that fits the work.'
      : 'Off. Agents that delegate pick harness and model on their own. Turn on to route them through this table.'}</p>
    {error && <p role="alert" className="settings-warning">{error}</p>}
    {config.roles.length === 0 && !adoption
      ? <div className="delegation-empty">
        <h3>No roles yet</h3>
        <p>Start with Attn's four maintained roles, then pick a model for each. Attn keeps their instructions current; the model choices stay yours.</p>
        <div className="delegation-actions center">
          <button type="button" className="settings-action primary" disabled={missing.length === 0} onClick={adopt}>Add Attn roles</button>
          <button type="button" className="settings-action" onClick={addRole}>+ Custom role</button>
        </div>
      </div>
      : <div className={`delegation-table ${config.enabled ? '' : 'dim'}`}>
        <div className="delegation-thead"><span /><span>Role</span><span>Runs on</span><span /></div>
        {config.roles.map(renderRole)}
        {renderFallback()}
        <div className="delegation-addrow">
          <button type="button" className="settings-action quiet" onClick={addRole}>+ Custom role</button>
          {missing.length > 0 && <button type="button" className="settings-action quiet" onClick={adopt}>{config.roles.some(role => role.builtin) ? `Restore Attn roles (${missing.length})` : 'Add Attn roles'}</button>}
        </div>
      </div>}
    {adoption && <AdoptionPanel config={config} templates={missing} names={templateName} adoption={adoption} onChange={setAdoption} onCancel={() => setAdoption(null)} onConfirm={confirmAdoption} />}
    {undo && <div role="status" className="delegation-undo"><span>{undo.label}.</span><button type="button" className="settings-action quiet" onClick={() => { void save(undo.previous); setUndo(null); }}>Undo</button></div>}
    {config.roles.length > 0 && <div className="delegation-foot">
      <span className={`delegation-live ${config.enabled ? '' : 'off'}`} role="status"><span className="delegation-dot" />{liveText}</span>
      <span className="delegation-spacer" />
      <span>Agents read this with <code>attn delegate roles</code>.</span>
    </div>}
    {popover && target && <DelegationModelPopover value={target.value} harnesses={state.harnesses} anchor={popover.anchor} onChange={target.set} onClose={() => setPopover(null)} loadModels={loadModels} />}
  </div>;
}
