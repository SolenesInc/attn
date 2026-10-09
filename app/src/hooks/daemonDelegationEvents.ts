import type { DelegationPreferences, DelegationRole, Harness, HarnessModel, TierDefaults } from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';

export interface DelegationSettingsState {
  preferences: DelegationPreferences;
  templates: DelegationRole[];
  expandedRoles: DelegationRole[];
  harnesses: Harness[];
  workflowSkillPaths: string[];
}
export interface HarnessModelCatalog { models: HarnessModel[]; detail: string; tier_defaults: TierDefaults }
interface DelegationEvent {
  event?: string;
  success?: boolean;
  error?: string;
  request_id?: string;
  preferences?: DelegationPreferences;
  templates?: DelegationRole[];
  expanded_roles?: DelegationRole[];
  workflow_skill_paths?: string[];
  harnesses?: Harness[];
  models?: HarnessModel[];
  detail?: string;
  tier_defaults?: TierDefaults;
}

export function handleDelegationDaemonEvent(event: DelegationEvent, pending: PendingRequests): boolean {
  if (event.event === 'delegation_preferences_result') {
    const extract = (value: DelegationEvent): DelegationSettingsState | undefined => value.preferences ? {
      preferences: value.preferences, templates: value.templates ?? [], expandedRoles: value.expanded_roles ?? [], harnesses: value.harnesses ?? [], workflowSkillPaths: value.workflow_skill_paths ?? [],
    } : undefined;
    if (!settlePendingRequest(pending, 'delegation_preferences_get', event, extract, 'Reading delegation preferences failed')) {
      settlePendingRequest(pending, 'delegation_preferences_save', event, extract, 'Saving delegation preferences failed');
    }
    return true;
  }
  if (event.event === 'harness_models_result') {
    settlePendingRequest(pending, 'harness_models', event,
      value => value.models ? { models: value.models, detail: value.detail ?? '', tier_defaults: value.tier_defaults ?? {} } : undefined,
      'Discovering models failed');
    return true;
  }
  if (event.event === 'delegation_preferences_changed') {
    useDelegationPreferencesPush.getState().push();
    return true;
  }
  return false;
}
