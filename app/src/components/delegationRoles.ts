import type { DelegationPreferences, DelegationRole, DelegationSelection } from '../types/generated';

export const FALLBACK = '__fallback';
export const emptySelection = (): DelegationSelection => ({ harness: '', provider: '', model: '', effort: '' });
export const newID = (prefix: string) => `${prefix}-${crypto.randomUUID()}`;
export const roleViewKey = (role: Pick<DelegationRole, 'id' | 'builtin'>) => `${role.id}:${role.builtin ?? ''}`;
export const firstLine = (text: string) => text.split('\n').find(line => line.trim())?.trim() ?? '';
export const complete = (selection: DelegationSelection) => selection.harness !== '';
export const defaultChoice = (role: DelegationRole) => role.choices.find(choice => choice.id === role.default_choice_id) ?? role.choices[0];
export const alternatives = (role: DelegationRole) => role.choices.filter(choice => choice.id !== role.default_choice_id);
export const liveRoles = (preferences: DelegationPreferences) => preferences.roles.filter(role => role.enabled && complete(defaultChoice(role).selection));
export const delegationLiveCount = (preferences: DelegationPreferences | null) => preferences?.enabled ? liveRoles(preferences).length : 0;

// The popover edits one selection at a time, addressed as `<role>/<choice>` or the fallback.
export function selectionAt(config: DelegationPreferences, key: string): { value: DelegationSelection; with: (selection: DelegationSelection) => DelegationPreferences } | null {
  if (key === FALLBACK) return { value: config.fallback.selection, with: selection => ({ ...config, fallback: { ...config.fallback, selection } }) };
  const [roleID, choiceID] = key.split('/');
  const role = config.roles.find(candidate => candidate.id === roleID);
  const choice = role?.choices.find(candidate => candidate.id === choiceID);
  if (!role || !choice) return null;
  const roles = (next: DelegationRole) => config.roles.map(candidate => candidate.id === next.id ? next : candidate);
  return { value: choice.selection, with: selection => ({ ...config, roles: roles({ ...role, choices: role.choices.map(candidate => candidate.id === choice.id ? { ...choice, selection } : candidate) }) }) };
}

// A maintained role renders through the daemon's expanded view; a custom role is its own view.
// The daemon lists configured roles before templates, and an adopted role shares its template's key.
export function roleViewer(expandedRoles: DelegationRole[]) {
  const views = new Map<string, DelegationRole>();
  for (const role of expandedRoles) if (!views.has(roleViewKey(role))) views.set(roleViewKey(role), role);
  return (role: DelegationRole) => role.builtin ? views.get(roleViewKey(role)) ?? role : role;
}
export const roleLabel = (view: DelegationRole) => view.name || view.builtin || view.id;
export const missingTemplates = (config: DelegationPreferences, templates: DelegationRole[]) => templates.filter(template => !config.roles.some(role => role.builtin === template.builtin));
export const freshAdoption = (templates: DelegationRole[]) => Object.fromEntries(templates.map(template => [template.builtin ?? template.id, 'new']));
// A custom role whose name starts with a maintained role's name is asked about before that role is added.
export const adoptionConflicts = (config: DelegationPreferences, templates: DelegationRole[], name: (template: DelegationRole) => string) =>
  templates.some(template => config.roles.some(role => !role.builtin && role.name.trim().toLowerCase().startsWith(name(template).toLowerCase())));

export function adoptMaintainedRoles(config: DelegationPreferences, templates: DelegationRole[], adoption: Record<string, string>): DelegationPreferences {
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
    while (occupied.has(roleID)) roleID = newID(template.id);
    occupied.add(roleID);
    additions.push({ ...structuredClone(template), id: roleID });
  }
  return { ...config, workflow_skill_enabled: true, roles: [...config.roles.filter(role => !replaced.has(role.id)), ...additions] };
}
