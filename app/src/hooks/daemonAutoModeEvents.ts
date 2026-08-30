
import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeEnvironmentInfo,
  AutoModeEnvironmentSlot,
  AutoModeEnvironmentSlotValue,
  AutoModeProposalInfo,
  AutoModeModelProvider,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';
import { useAutoModePushStore } from '../store/autoMode';

export type {
  AutoModeModelProvider,
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeEnvironmentInfo,
  AutoModeEnvironmentSlot,
  AutoModeProposalInfo,
};

export interface AutoModeState {
  config: AutoModeConfigInfo;

  proposals: AutoModeProposalInfo[];
  denials: AutoModeDenialInfo[];
  environmentSlots: AutoModeEnvironmentSlot[];
}

export interface AutoModePatternEdit {
  config: AutoModeConfigInfo;
}

export interface AutoModeModelCatalog {
  providers: AutoModeModelProvider[];
  problem: string | null;
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

const emptyConfig = (): AutoModeConfigInfo => ({
  enabled_default: false,
  environment: { slots: [], notes: [] },
  allow: [],
  hard_deny: [],
  shipped_hard_deny: [],
  models: [],
});

const toModelCatalog = (event: AutoModeDaemonEvent): AutoModeModelCatalog => ({
  providers: list<AutoModeModelProvider>(event.providers),
  problem: typeof event.problem === 'string' ? event.problem : null,
});

const toEnvironment = (value: unknown): AutoModeEnvironmentInfo => {
  if (typeof value !== 'object' || value === null) return { slots: [], notes: [] };
  const raw = value as Record<string, unknown>;
  return {
    slots: list<AutoModeEnvironmentSlotValue>(raw.slots),
    notes: list<string>(raw.notes),
  };
};

const toConfig = (value: unknown): AutoModeConfigInfo => {
  if (typeof value !== 'object' || value === null) return emptyConfig();
  const raw = value as Record<string, unknown>;
  return {
    enabled_default: raw.enabled_default === true,
    environment: toEnvironment(raw.environment),
    allow: list<string>(raw.allow),
    hard_deny: list<string>(raw.hard_deny),
    shipped_hard_deny: list<string>(raw.shipped_hard_deny),
    models: list<string>(raw.models),
  };
};

const toState = (event: AutoModeDaemonEvent): AutoModeState => ({
  config: toConfig(event.config),
  proposals: list<AutoModeProposalInfo>(event.proposals),
  denials: list<AutoModeDenialInfo>(event.denials),
  environmentSlots: list<AutoModeEnvironmentSlot>(event.environment_slots),
});

const toPatternEdit = (event: AutoModeDaemonEvent): AutoModePatternEdit | undefined =>
  event.config === undefined ? undefined : { config: toConfig(event.config) };

const toPromotion = (event: AutoModeDaemonEvent): AutoModePromotion => ({
  proposal: (event.proposal as AutoModeProposalInfo | undefined) ?? null,
  config: event.config === undefined ? null : toConfig(event.config),
});

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
    // One event answers both edits, so the settle is tried under each command's key;
    // only the one actually in flight has a waiter.
    case 'automode_pattern_result': {
      const settled = settlePendingRequest(
        pending,
        'automode_pattern_add',
        event,
        toPatternEdit,
        'Adding the pattern failed',
      );
      if (!settled) {
        settlePendingRequest(
          pending,
          'automode_pattern_remove',
          event,
          toPatternEdit,
          'Removing the pattern failed',
        );
      }
      return true;
    }
    case 'automode_env_set_result': {
      const settledSlot = settlePendingRequest(
        pending,
        'automode_env_slot',
        event,
        toPatternEdit,
        'Saving what the classifier knows about this machine failed',
      );
      if (!settledSlot) {
        settlePendingRequest(
          pending,
          'automode_env_notes',
          event,
          toPatternEdit,
          'Saving your notes about this machine failed',
        );
      }
      return true;
    }
    case 'automode_model_set_result':
      settlePendingRequest(
        pending,
        'automode_model_set',
        event,
        toPatternEdit,
        'Saving the models failed',
      );
      return true;
    case 'automode_models_result':
      settlePendingRequest(
        pending,
        'automode_models',
        event,
        toModelCatalog,
        'Asking pi which models it can reach failed',
      );
      return true;
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
