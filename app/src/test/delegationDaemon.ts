import { openSection } from './settings';
import type { CommandMessage, EventMessage } from './protocol';
import type { Reply, ScriptedDaemon } from './scriptedDaemon';

type Preferences = CommandMessage<'delegation_preferences_save'>['preferences'];
export type Role = Preferences['roles'][number];
type Harness = NonNullable<EventMessage<'delegation_preferences_result'>['harnesses']>[number];

const REVISION_CONFLICT = 'delegation preferences changed; reload before saving or choosing a role';
export const emptySelection = () => ({ harness: '', provider: '', model: '', effort: '' });
const codex: Harness = { id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true };

export interface DelegationTable {
  roles?: Role[];
  enabled?: boolean;
  templates?: Role[];
  expanded?: (roles: Role[]) => Role[];
  harnesses?: Harness[];
}

interface DelegationServer {
  preferences: Preferences;
  harnesses: Harness[];
  changeElsewhere(change?: (preferences: Preferences) => void): void;
  announce(revision?: number): void;
  holdSaves(): void;
  answerHeldSave(): void;
  resumeSaves(): void;
  holdLoads(): void;
  resumeLoads(): void;
  failLoads(reason: string): void;
}

type Save = CommandMessage<'delegation_preferences_save'>;
type Load = CommandMessage<'delegation_preferences_get'>;

function scriptDelegation(daemon: ScriptedDaemon, { roles = [], enabled = roles.length > 0, templates = [], expanded = (current) => current, harnesses = [codex] }: DelegationTable) {
  const heldSaves: Save[] = [];
  const heldLoads: Load[] = [];
  let holdingSaves = false;
  let holdingLoads = false;
  let loadFailure = '';

  const table = (requestId: string): Reply => ({
    event: 'delegation_preferences_result',
    request_id: requestId,
    success: true,
    preferences: server.preferences,
    templates,
    expanded_roles: expanded(server.preferences.roles),
    harnesses: server.harnesses,
    workflow_skill_paths: [],
  });
  const refusal = (requestId: string, error: string): Reply => ({ event: 'delegation_preferences_result', request_id: requestId, success: false, error });
  const answerSave = ({ request_id, preferences }: Save): Reply => {
    if (preferences.revision !== server.preferences.revision) return refusal(request_id, REVISION_CONFLICT);
    server.preferences = { ...preferences, revision: preferences.revision + 1 };
    return table(request_id);
  };
  const answerLoad = ({ request_id }: Load): Reply => (loadFailure ? refusal(request_id, loadFailure) : table(request_id));

  const server: DelegationServer = {
    preferences: { enabled, revision: 0, workflow_skill_enabled: false, roles, fallback: { selection: emptySelection(), instructions: '' } },
    harnesses,
    changeElsewhere(change = () => {}) {
      const next = structuredClone(server.preferences);
      change(next);
      server.preferences = { ...next, revision: next.revision + 1 };
    },
    announce(revision = server.preferences.revision) {
      daemon.emit({ event: 'delegation_preferences_changed', revision });
    },
    holdSaves() { holdingSaves = true; },
    answerHeldSave() { const save = heldSaves.shift(); if (save) daemon.replyTo(save, answerSave(save)); },
    resumeSaves() { holdingSaves = false; for (const save of heldSaves.splice(0)) daemon.replyTo(save, answerSave(save)); },
    holdLoads() { holdingLoads = true; },
    resumeLoads() { holdingLoads = false; for (const load of heldLoads.splice(0)) daemon.replyTo(load, answerLoad(load)); },
    failLoads(reason) { loadFailure = reason; },
  };

  daemon.on('delegation_preferences_get', (load) => (holdingLoads ? void heldLoads.push(load) : answerLoad(load)));
  daemon.on('delegation_preferences_save', (save) => (holdingSaves ? void heldSaves.push(save) : answerSave(save)));
  daemon.on('delegation_models', () => ({
    event: 'delegation_models_result',
    success: true,
    models: [{ harness: 'codex', provider: '', id: 'model-a', name: 'Everyday model', description: '', detail: '', effort_support: 'supported', effort_levels: ['medium', 'high'], access: 'unknown' }],
    detail: 'Reported by Codex',
  }));
  return server;
}

export async function openDelegationSettings(table: DelegationTable = {}) {
  let server!: DelegationServer;
  const daemon = await openSection('delegation', {}, (scripted) => { server = scriptDelegation(scripted, table); });
  const saves = () => daemon.sentOf('delegation_preferences_save');
  return {
    daemon,
    server,
    saves,
    loads: () => daemon.sentOf('delegation_preferences_get'),
    lastSaved: () => saves()[saves().length - 1].preferences,
  };
}
