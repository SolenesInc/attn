import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeEnvironmentInfo,
  AutoModeEnvironmentSlot,
  AutoModeEnvironmentSlotValue,
  AutoModeNetworkInfo,
  AutoModeProposalInfo,
  AutoModeRuleInfo,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';
import { useAutoModePushStore } from '../store/autoMode';

export type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeEnvironmentInfo,
  AutoModeEnvironmentSlot,
  AutoModeNetworkInfo,
  AutoModeProposalInfo,
  AutoModeRuleInfo,
};

export interface AutoModeState {
  config: AutoModeConfigInfo;

  proposals: AutoModeProposalInfo[];
  denials: AutoModeDenialInfo[];
  environmentSlots: AutoModeEnvironmentSlot[];
}

// A policy edit names only what it moves; a field left out stays as it stands.
export interface AutoModePolicyEdit {
  approvalPolicy?: string;
  sandboxMode?: string;
  allowLocalBinding?: boolean;
}

export interface AutoModeConfigEdit {
  config: AutoModeConfigInfo;
}

export interface AutoModePromotion {
  proposal: AutoModeProposalInfo | null;
  config: AutoModeConfigInfo | null;
}

interface AutoModeDaemonEvent {
  event?: string;
  success?: boolean;
  error?: string;
  [key: string]: unknown;
}

const list = <T,>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : []);

const emptyNetwork = (): AutoModeNetworkInfo => ({
  enabled: false,
  allowed_domains: [],
  denied_domains: [],
  allow_local_binding: false,
});

const emptyConfig = (): AutoModeConfigInfo => ({
  enabled_default: false,
  approval_policy: '',
  sandbox_mode: '',
  environment: { slots: [], notes: [] },
  rules: [],
  shipped_rules: [],
  network: emptyNetwork(),
  shipped_denied_domains: [],
  legacy_patterns: [],
});

const toEnvironment = (value: unknown): AutoModeEnvironmentInfo => {
  if (typeof value !== 'object' || value === null) return { slots: [], notes: [] };
  const raw = value as Record<string, unknown>;
  return {
    slots: list<AutoModeEnvironmentSlotValue>(raw.slots),
    notes: list<string>(raw.notes),
  };
};

const toNetwork = (value: unknown): AutoModeNetworkInfo => {
  if (typeof value !== 'object' || value === null) return emptyNetwork();
  const raw = value as Record<string, unknown>;
  return {
    enabled: raw.enabled === true,
    allowed_domains: list<string>(raw.allowed_domains),
    denied_domains: list<string>(raw.denied_domains),
    allow_local_binding: raw.allow_local_binding === true,
  };
};

const toConfig = (value: unknown): AutoModeConfigInfo => {
  if (typeof value !== 'object' || value === null) return emptyConfig();
  const raw = value as Record<string, unknown>;
  return {
    enabled_default: raw.enabled_default === true,
    approval_policy: typeof raw.approval_policy === 'string' ? raw.approval_policy : '',
    sandbox_mode: typeof raw.sandbox_mode === 'string' ? raw.sandbox_mode : '',
    environment: toEnvironment(raw.environment),
    rules: list<AutoModeRuleInfo>(raw.rules),
    shipped_rules: list<AutoModeRuleInfo>(raw.shipped_rules),
    network: toNetwork(raw.network),
    shipped_denied_domains: list<string>(raw.shipped_denied_domains),
    legacy_patterns: list<string>(raw.legacy_patterns),
  };
};

const toState = (event: AutoModeDaemonEvent): AutoModeState => ({
  config: toConfig(event.config),
  proposals: list<AutoModeProposalInfo>(event.proposals),
  denials: list<AutoModeDenialInfo>(event.denials),
  environmentSlots: list<AutoModeEnvironmentSlot>(event.environment_slots),
});

const toConfigEdit = (event: AutoModeDaemonEvent): AutoModeConfigEdit | undefined =>
  event.config === undefined ? undefined : { config: toConfig(event.config) };

const toPromotion = (event: AutoModeDaemonEvent): AutoModePromotion => ({
  proposal: (event.proposal as AutoModeProposalInfo | undefined) ?? null,
  config: event.config === undefined ? null : toConfig(event.config),
});

// One event answers every config edit, so the settle is tried under each command's key;
// only the one actually in flight has a waiter.
const configEditCommands: [string, string][] = [
  ['automode_rule_add', 'Adding the rule failed'],
  ['automode_rule_remove', 'Removing the rule failed'],
  ['automode_host_add', 'Adding the host failed'],
  ['automode_host_remove', 'Removing the host failed'],
  ['automode_policy_set', 'Saving the approval policy failed'],
];

/** Settles an auto mode result, or returns false for an event this module does not own. */
export function handleAutoModeDaemonEvent(
  event: AutoModeDaemonEvent,
  pending: PendingRequests,
): boolean {
  switch (event.event) {
    case 'automode_state_result':
      settlePendingRequest(
        pending,
        'automode_get',
        event,
        toState,
        'Reading auto mode failed',
      );
      return true;
    case 'automode_promote_result':
      settlePendingRequest(
        pending,
        'automode_promote',
        event,
        toPromotion,
        'Promoting the proposal failed',
      );
      return true;
    case 'automode_config_result': {
      for (const [command, whenItFails] of configEditCommands) {
        if (settlePendingRequest(pending, command, event, toConfigEdit, whenItFails)) break;
      }
      return true;
    }
    case 'automode_env_set_result': {
      const settledSlot = settlePendingRequest(
        pending,
        'automode_env_slot',
        event,
        toConfigEdit,
        'Saving what the reviewer knows about this machine failed',
      );
      if (!settledSlot) {
        settlePendingRequest(
          pending,
          'automode_env_notes',
          event,
          toConfigEdit,
          'Saving your notes about this machine failed',
        );
      }
      return true;
    }
    case 'automode_state_changed':
      useAutoModePushStore.getState().push(toState(event));
      return true;
    case 'automode_discard_result':
      settlePendingRequest(
        pending,
        'automode_discard',
        event,
        toPromotion,
        'Discarding the proposal failed',
      );
      return true;
    default:
      return false;
  }
}
