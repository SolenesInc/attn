import type { PtyAttachPolicy } from './bridge';

export interface ExistingRuntimeAttachOptions {
  policy: Extract<
    PtyAttachPolicy,
    'relaunch_restore' | 'same_app_remount' | 'revive'
  >;
  forceResizeBeforeAttach?: boolean;
}

export function normalizeAttachPolicy(
  policy?: PtyAttachPolicy,
): Extract<PtyAttachPolicy, 'relaunch_restore' | 'same_app_remount' | 'revive'> {
  if (policy === 'relaunch_restore' || policy === 'revive') {
    return policy;
  }
  return 'same_app_remount';
}

export function isAlreadyExistsError(error: unknown): boolean {
  const message = error instanceof Error ? error.message.toLowerCase() : String(error).toLowerCase();
  return message.includes('already exists');
}
