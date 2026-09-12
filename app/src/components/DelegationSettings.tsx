import { useState } from 'react';
import type { DelegationChoice, DelegationPreferences, DelegationRole, DelegationSelection, DelegationHarness } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { DelegationPreferencesPolicy } from '../hooks/useDelegationPreferences';
import { knownModelName } from '../hooks/useDelegationModelCatalog';
import { DelegationRoleIcon } from './DelegationRoleIcon';
import { DelegationModelPopover, type Anchor } from './DelegationModelPopover';
import { FALLBACK, adoptMaintainedRoles, adoptionConflicts, alternatives, complete, defaultChoice, emptySelection, firstLine, freshAdoption, liveRoles, missingTemplates, newID, roleLabel, roleViewer, selectionAt } from './delegationRoles';
import './DelegationSettings.css';

type PopoverTarget = { key: string; anchor: Anchor };
type Undo = { label: string; previous: DelegationPreferences; generation: number };

const DELEGATION_ICONS = ['search', 'diamond', 'code', 'arrow', 'list', 'bug', 'spark', 'circle'] as const;
const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
const leadText = (enabled: boolean) => enabled
  ? 'Roles guide agents that delegate. Each role has a model; an agent reads this table and picks the row that fits the work.'
  : 'Off. Agents that delegate pick harness and model on their own. Turn on to route them through this table.';

// A deletion can be undone until the next edit; a save moves generation by one, so the undo lives while nothing else moves it.
function useUndo(generation: number) {
  const [undo, setUndo] = useState<Undo | null>(null);
  const live = undo && undo.generation === generation ? undo : null;
  const remember = (label: string, previous: DelegationPreferences) => setUndo({ label, previous: structuredClone(previous), generation: generation + 1 });
  return { undo: live, remember, forget: () => setUndo(null) };
}

// One row and one of its alternatives open at a time; opening another row closes both.
function useExpansion() {
  const [expanded, setExpanded] = useState<string | null>(null);
  const [expandedAlt, setExpandedAlt] = useState<string | null>(null);
  const toggle = (key: string) => { setExpanded(current => current === key ? null : key); setExpandedAlt(null); };
  const expand = (key: string) => { setExpanded(key); setExpandedAlt(null); };
  const closeAlt = (id: string) => setExpandedAlt(current => current === id ? null : current);
  return { expanded, expandedAlt, toggle, expand, setExpandedAlt, closeAlt, collapse: () => setExpanded(null) };
}

function useModelPopover() {
  const [popover, setPopover] = useState<PopoverTarget | null>(null);
  const open = (key: string, anchor: Anchor) => setPopover(current => current?.key === key ? null : { key, anchor });
  return { popover, key: popover?.key ?? null, open, close: () => setPopover(null) };
}

function LoadingState({ error, onRetry }: { error: string; onRetry: () => void }) {
  if (!error) return <div role="status">Loading delegation preferences…</div>;
  return <div role="status">{error}<button type="button" className="settings-action" onClick={onRetry}>Retry</button></div>;
}

export function DelegationSwitch({ policy }: { policy: DelegationPreferencesPolicy }) {
  const { preferences, save } = policy;
  if (!preferences) return null;
  return <button type="button" role="switch" aria-checked={preferences.enabled} aria-label="Delegation preferences" className="delegation-switch" onClick={() => void save({ ...preferences, enabled: !preferences.enabled })}>
    <span className="delegation-switch-track" /><span>{preferences.enabled ? 'On' : 'Off'}</span>
  </button>;
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

function RoleEditor({ role, onUpdate }: { role: DelegationRole; onUpdate: (role: DelegationRole) => void }) {
  return <>
    <TextField id={`name-${role.id}`} label="Name" value={role.name} multiline={false} onCommit={name => { if (name.trim()) onUpdate({ ...role, name: name.trim() }); }} />
    <div className="delegation-icons" role="group" aria-label="Role icon">
      <button type="button" className={`delegation-icon pick ${role.icon === '' ? 'active' : ''}`} aria-label="Initial as icon" aria-pressed={role.icon === ''} onClick={() => onUpdate({ ...role, icon: '' })}><DelegationRoleIcon icon="" name={role.name} /></button>
      {DELEGATION_ICONS.map(icon => <button key={icon} type="button" className={`delegation-icon pick ${role.icon === icon ? 'active' : ''}`} aria-label={`${icon} icon`} aria-pressed={role.icon === icon} onClick={() => onUpdate({ ...role, icon })}><DelegationRoleIcon icon={icon} name={role.name} /></button>)}
    </div>
    <TextField id={`desc-${role.id}`} label="When to choose this role" value={role.description} placeholder="Describe the work this role fits. The agent reads this to pick a role, so say what it looks like and what it is not for." hint="Prose, as long as it needs to be. The first line is what the table shows." onCommit={description => onUpdate({ ...role, description })} />
    <TextField id={`ins-${role.id}`} label="Instructions" value={role.instructions} onCommit={instructions => onUpdate({ ...role, instructions })} />
    <TextField id={`stop-${role.id}`} label="Stops when" value={role.stopping_point} onCommit={stopping_point => onUpdate({ ...role, stopping_point })} />
  </>;
}

function AlternativeRow({ alt, harnesses, open, popoverOpen, onToggle, onOpenPopover, onUpdate, onRemove }: {
  alt: DelegationChoice;
  harnesses: DelegationHarness[];
  open: boolean;
  popoverOpen: boolean;
  onToggle: () => void;
  onOpenPopover: (anchor: Anchor) => void;
  onUpdate: (choice: DelegationChoice) => void;
  onRemove: () => void;
}) {
  const name = alt.name || 'alternative';
  const when = firstLine(alt.when);
  return <div>
    <div className="delegation-alt">
      <span />
      <button type="button" className="delegation-alt-who" aria-label={alt.name || 'Unnamed alternative'} aria-expanded={open} onClick={onToggle}>
        <span className="delegation-alt-name">{alt.name || 'Unnamed alternative'}</span>
        <span className={`delegation-alt-when ${when ? '' : 'blank'}`}><span className="w">when</span>{when || 'No condition yet. The agent cannot pick this.'}</span>
      </button>
      <ModelCell selection={alt.selection} harnesses={harnesses} label={`Model for ${name}`} open={popoverOpen} onOpen={onOpenPopover} />
      <button type="button" className="delegation-rm" aria-label={`Remove ${name}`} onClick={onRemove} title={`Remove ${name}`}>×</button>
    </div>
    {open && <div className="delegation-alt-edit">
      <TextField id={`altname-${alt.id}`} label="Name" value={alt.name} multiline={false} placeholder="Short label, shown to the agent with the condition" onCommit={next => onUpdate({ ...alt, name: next.trim() || 'Alternative' })} />
      <TextField id={`when-${alt.id}`} label="When to use this instead of the default" value={alt.when} placeholder="Describe the work this alternative fits. The agent reads this to decide, so say what it looks like and what it is not for." hint="Prose, as long as it needs to be. The first line is what the table shows." onCommit={next => onUpdate({ ...alt, when: next })} />
    </div>}
  </div>;
}

type RoleRowProps = {
  role: DelegationRole;
  view: DelegationRole;
  harnesses: DelegationHarness[];
  open: boolean;
  expandedAlt: string | null;
  popoverKey: string | null;
  onToggle: () => void;
  onExpandAlt: (id: string | null) => void;
  onOpenPopover: (key: string, anchor: Anchor) => void;
  onUpdate: (role: DelegationRole) => void;
  onCopy: () => void;
  onDelete: () => void;
  onRemoveAlt: (alt: DelegationChoice) => void;
};

function RoleRow({ role, view: v, harnesses, open, expandedAlt, popoverKey, onToggle, onExpandAlt, onOpenPopover, onUpdate, onCopy, onDelete, onRemoveAlt }: RoleRowProps) {
  const main = defaultChoice(role);
  const alts = alternatives(role);
  const needs = role.enabled && !complete(main.selection);
  const readOnly = Boolean(role.builtin);
  const cellKey = `${role.id}/${main.id}`;
  const updateChoice = (next: DelegationChoice) => onUpdate({ ...role, choices: role.choices.map(choice => choice.id === next.id ? next : choice) });
  const addAlternative = () => {
    const choice: DelegationChoice = { id: newID('choice'), name: 'Alternative', when: '', selection: structuredClone(main.selection) };
    onUpdate({ ...role, choices: [...role.choices, choice] });
    onExpandAlt(choice.id);
  };
  return <div className={`delegation-row ${role.enabled ? '' : 'off'} ${open ? 'open' : ''}`} data-role-id={role.id}>
    <div className="delegation-row-main">
      <span className="delegation-icon"><DelegationRoleIcon icon={v.icon} name={v.name} /></span>
      <button type="button" className="delegation-who" aria-label={v.name} aria-expanded={open} onClick={onToggle}>
        <span className="delegation-name">{v.name}{readOnly && <span className="delegation-tag">Attn</span>}{!role.enabled && <span className="delegation-tag">Off</span>}{needs && <span className="delegation-tag needs">Needs a model</span>}</span>
        <span className="delegation-desc">{firstLine(v.description)}</span>
      </button>
      <span className="delegation-cell">
        <ModelCell selection={main.selection} harnesses={harnesses} label={`Model for ${v.name}`} open={popoverKey === cellKey} onOpen={anchor => onOpenPopover(cellKey, anchor)} />
        {alts.length > 0 && !open && <button type="button" className="delegation-altchip" onClick={onToggle}>+{alts.length} alternative{alts.length === 1 ? '' : 's'}</button>}
      </span>
      <button type="button" className={`delegation-more ${open ? 'open' : ''}`} aria-label={`${v.name} details`} aria-expanded={open} onClick={onToggle}>···</button>
    </div>
    {open && <>
      <div className="delegation-details">
        {readOnly
          ? <>
            <ReadOnlyField label="Instructions" value={v.instructions} note="maintained by Attn, updates with releases" />
            <ReadOnlyField label="Stops when" value={v.stopping_point} />
          </>
          : <RoleEditor role={role} onUpdate={onUpdate} />}
      </div>
      {alts.map(alt => <AlternativeRow key={alt.id} alt={alt} harnesses={harnesses} open={expandedAlt === alt.id} popoverOpen={popoverKey === `${role.id}/${alt.id}`}
        onToggle={() => onExpandAlt(expandedAlt === alt.id ? null : alt.id)} onOpenPopover={anchor => onOpenPopover(`${role.id}/${alt.id}`, anchor)} onUpdate={updateChoice} onRemove={() => onRemoveAlt(alt)} />)}
      <div className="delegation-alt add"><span /><button type="button" className="settings-action quiet" onClick={addAlternative}>+ Alternative model</button></div>
      <div className="delegation-details actions"><div className="delegation-actions">
        {readOnly && <button type="button" className="settings-action" onClick={onCopy}>Make an editable copy</button>}
        <span className="delegation-spacer" />
        <button type="button" className="settings-action quiet" onClick={() => onUpdate({ ...role, enabled: !role.enabled })}>{role.enabled ? 'Turn off' : 'Turn on'}</button>
        <button type="button" className="settings-action quiet danger" onClick={onDelete}>Delete</button>
      </div></div>
    </>}
  </div>;
}

function FallbackRow({ fallback, harnesses, open, popoverOpen, onToggle, onOpenPopover, onChange }: {
  fallback: DelegationPreferences['fallback'];
  harnesses: DelegationHarness[];
  open: boolean;
  popoverOpen: boolean;
  onToggle: () => void;
  onOpenPopover: (anchor: Anchor) => void;
  onChange: (fallback: DelegationPreferences['fallback']) => void;
}) {
  return <div className={`delegation-row ${open ? 'open' : ''}`} data-role-id="fallback">
    <div className="delegation-row-main">
      <span className="delegation-icon fallback"><DelegationRoleIcon icon="arrow" name="Anything else" /></span>
      <button type="button" className="delegation-who" aria-label="Anything else" aria-expanded={open} onClick={onToggle}>
        <span className="delegation-name">Anything else</span>
        <span className="delegation-desc">When no role fits the work. Agents use this instead of choosing a model themselves.</span>
      </button>
      <span className="delegation-cell"><ModelCell selection={fallback.selection} harnesses={harnesses} label="Model for anything else" open={popoverOpen} onOpen={onOpenPopover} /></span>
      <button type="button" className={`delegation-more ${open ? 'open' : ''}`} aria-label="Anything else details" aria-expanded={open} onClick={onToggle}>···</button>
    </div>
    {open && <div className="delegation-details">
      <TextField id="fb-ins" label="Instructions (optional)" value={fallback.instructions} placeholder="Anything every unmatched delegation should hear." onCommit={instructions => onChange({ ...fallback, instructions })} />
    </div>}
  </div>;
}

function EmptyPanel({ canAdopt, onAdopt, onAdd }: { canAdopt: boolean; onAdopt: () => void; onAdd: () => void }) {
  return <div className="delegation-empty">
    <h3>No roles yet</h3>
    <p>Start with Attn's four maintained roles, then pick a model for each. Attn keeps their instructions current; the model choices stay yours.</p>
    <div className="delegation-actions center">
      <button type="button" className="settings-action primary" disabled={!canAdopt} onClick={onAdopt}>Add Attn roles</button>
      <button type="button" className="settings-action" onClick={onAdd}>+ Custom role</button>
    </div>
  </div>;
}

function AddRow({ config, missing, onAdd, onAdopt }: { config: DelegationPreferences; missing: DelegationRole[]; onAdd: () => void; onAdopt: () => void }) {
  const restore = config.roles.some(role => role.builtin);
  return <div className="delegation-addrow">
    <button type="button" className="settings-action quiet" onClick={onAdd}>+ Custom role</button>
    {missing.length > 0 && <button type="button" className="settings-action quiet" onClick={onAdopt}>{restore ? `Restore Attn roles (${missing.length})` : 'Add Attn roles'}</button>}
  </div>;
}

function TableFoot({ config }: { config: DelegationPreferences }) {
  const live = liveRoles(config).length;
  const needs = config.roles.filter(role => role.enabled && !complete(defaultChoice(role).selection)).length;
  const off = config.roles.filter(role => !role.enabled).length;
  const text = !config.enabled
    ? 'Off. Agents choose harness and model themselves. Your table is kept.'
    : `Agents see ${live} of ${plural(config.roles.length, 'role')}${complete(config.fallback.selection) ? ' and the fallback' : ''}.${needs ? ` ${needs} need${needs === 1 ? 's' : ''} a model.` : ''}${off ? ` ${off} off.` : ''}`;
  return <div className="delegation-foot">
    <span className={`delegation-live ${config.enabled ? '' : 'off'}`} role="status"><span className="delegation-dot" />{text}</span>
    <span className="delegation-spacer" />
    <span>Agents read this with <code>attn delegate roles</code>.</span>
  </div>;
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
  const { state, preferences: config, error, generation, reload, save } = policy;
  const [adoption, setAdoption] = useState<Record<string, string> | null>(null);
  const { undo, remember, forget } = useUndo(generation);
  const rows = useExpansion();
  const picker = useModelPopover();
  if (!config || !state) return <LoadingState error={error} onRetry={() => void reload()} />;

  const view = roleViewer(state.expandedRoles);
  const commit = (next: DelegationPreferences) => { void save(next); };
  const commitUndoable = (next: DelegationPreferences, label: string) => { void save(next); remember(label, config); };
  const withRoles = (roles: DelegationRole[]) => ({ ...config, roles });
  const updateRole = (next: DelegationRole) => commit(withRoles(config.roles.map(role => role.id === next.id ? next : role)));

  const addRole = () => {
    const role: DelegationRole = { id: newID('role'), name: 'New role', icon: '', enabled: true, description: '', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Default', when: '', selection: emptySelection() }] };
    commit(withRoles([...config.roles, role]));
    rows.expand(role.id);
  };
  const copyRole = (role: DelegationRole) => {
    const v = view(role);
    const copy: DelegationRole = { ...structuredClone(v), id: newID('role'), builtin: undefined, name: `${v.name} (custom)` };
    const index = config.roles.findIndex(r => r.id === role.id);
    commit(withRoles([...config.roles.slice(0, index + 1), copy, ...config.roles.slice(index + 1)]));
    rows.expand(copy.id);
  };
  const deleteRole = (role: DelegationRole) => {
    commitUndoable(withRoles(config.roles.filter(r => r.id !== role.id)), `Deleted ${view(role).name}`);
    rows.collapse();
  };
  const removeAlternative = (role: DelegationRole, alt: DelegationChoice) => {
    const name = alt.name || 'alternative';
    commitUndoable(withRoles(config.roles.map(r => r.id === role.id ? { ...role, choices: role.choices.filter(c => c.id !== alt.id) } : r)), `Removed ${name} from ${view(role).name}`);
    rows.closeAlt(alt.id);
  };
  const missing = missingTemplates(config, state.templates);
  const templateName = (template: DelegationRole) => roleLabel(view(template));
  const adopt = () => {
    if (adoptionConflicts(config, missing, templateName)) { setAdoption(freshAdoption(missing)); return; }
    forget();
    void save(adoptMaintainedRoles(config, missing, {}), true);
  };
  const confirmAdoption = () => { forget(); void save(adoptMaintainedRoles(config, missing, adoption ?? {}), true); setAdoption(null); };
  const target = picker.key === null ? null : selectionAt(config, picker.key);

  return <div className="delegation-settings" data-testid="delegation-settings">
    <p className={`delegation-lead ${config.enabled ? '' : 'off'}`}>{leadText(config.enabled)}</p>
    {error && <p role="alert" className="settings-warning">{error}</p>}
    <div className={`delegation-table ${config.enabled ? '' : 'dim'}`}>
      <div className="delegation-thead"><span /><span>Role</span><span>Runs on</span><span /></div>
      {config.roles.length === 0 && !adoption && <EmptyPanel canAdopt={missing.length > 0} onAdopt={adopt} onAdd={addRole} />}
      {config.roles.map(role => <RoleRow key={role.id} role={role} view={view(role)} harnesses={state.harnesses} open={rows.expanded === role.id} expandedAlt={rows.expandedAlt} popoverKey={picker.key}
        onToggle={() => rows.toggle(role.id)} onExpandAlt={rows.setExpandedAlt} onOpenPopover={picker.open} onUpdate={updateRole} onCopy={() => copyRole(role)} onDelete={() => deleteRole(role)} onRemoveAlt={alt => removeAlternative(role, alt)} />)}
      <FallbackRow fallback={config.fallback} harnesses={state.harnesses} open={rows.expanded === FALLBACK} popoverOpen={picker.key === FALLBACK} onToggle={() => rows.toggle(FALLBACK)} onOpenPopover={anchor => picker.open(FALLBACK, anchor)} onChange={fallback => commit({ ...config, fallback })} />
      {config.roles.length > 0 && <AddRow config={config} missing={missing} onAdd={addRole} onAdopt={adopt} />}
    </div>
    {adoption && <AdoptionPanel config={config} templates={missing} names={templateName} adoption={adoption} onChange={setAdoption} onCancel={() => setAdoption(null)} onConfirm={confirmAdoption} />}
    {undo && <div role="status" className="delegation-undo"><span>{undo.label}.</span><button type="button" className="settings-action quiet" onClick={() => { void save(undo.previous); forget(); }}>Undo</button></div>}
    {config.roles.length > 0 && <TableFoot config={config} />}
    {picker.popover && target && <DelegationModelPopover value={target.value} harnesses={state.harnesses} anchor={picker.popover.anchor} onChange={selection => commit(target.with(selection))} onClose={picker.close} loadModels={loadModels} />}
  </div>;
}
