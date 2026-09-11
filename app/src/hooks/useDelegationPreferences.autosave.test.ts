import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { useDelegationPreferences } from './useDelegationPreferences';
import { createMockDaemon } from '../test/mocks/daemon';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';
import type { DelegationSettingsState } from './daemonDelegationEvents';
import type { DelegationPreferences } from '../types/generated';

const selection = () => ({ harness: '', provider: '', model: '', effort: '' });
const preferences = (revision: number, instructions = ''): DelegationPreferences => ({ enabled: true, revision, workflow_skill_enabled: false, roles: [], fallback: { selection: selection(), instructions } });

function setup() {
  let server: DelegationSettingsState = { preferences: preferences(0), templates: [], expandedRoles: [], harnesses: [], workflowSkillPaths: [] };
  const daemon = createMockDaemon();
  daemon.setResponse('load', () => structuredClone(server));
  let release: (() => void) | null = null;
  daemon.setResponse('save', async (args: unknown[]) => {
    const value = args[0] as DelegationPreferences;
    if (release === null) await new Promise<void>(resolve => { release = resolve; });
    if (value.revision !== server.preferences.revision) throw new Error('delegation preferences changed; reload before saving or choosing a role');
    server = { ...server, preferences: { ...structuredClone(value), revision: value.revision + 1 } };
    return structuredClone(server);
  });
  const load = daemon.createRequest<DelegationSettingsState>('load');
  const save = daemon.createRequest<DelegationSettingsState>('save');
  const hook = renderHook(() => useDelegationPreferences(true, load, save));
  return { daemon, hook, server: () => server, bump: () => { server = { ...server, preferences: { ...server.preferences, revision: server.preferences.revision + 1 } }; }, releaseFirst: () => { const r = release; release = () => {}; r?.(); } };
}

afterEach(() => useDelegationPreferencesPush.getState().clear());

it('collapses edits made during a save into one follow-up carrying the returned revision', async () => {
  const { daemon, hook, server, releaseFirst } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  act(() => { void hook.result.current.save(preferences(0, 'one')); });
  act(() => { void hook.result.current.save(preferences(0, 'two')); });
  act(() => { void hook.result.current.save(preferences(0, 'three')); });
  expect(hook.result.current.preferences?.fallback.instructions).toBe('three');
  expect(hook.result.current.busy).toBe(true);
  releaseFirst();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(daemon.getCalls('save').map(call => [(call.args[0] as DelegationPreferences).revision, (call.args[0] as DelegationPreferences).fallback.instructions])).toEqual([[0, 'one'], [1, 'three']]);
  expect(server().preferences.fallback.instructions).toBe('three');
  expect(hook.result.current.preferences?.revision).toBe(2);
  expect(daemon.getCalls('load')).toHaveLength(1);
});

it('drops local edits and reloads when the daemon reports a conflict', async () => {
  const { daemon, hook, bump, releaseFirst } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  bump();
  act(() => { void hook.result.current.save(preferences(0, 'mine')); });
  releaseFirst();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(hook.result.current.error).toContain('reload before saving');
  expect(hook.result.current.preferences?.fallback.instructions).toBe('');
  expect(hook.result.current.preferences?.revision).toBe(1);
  expect(daemon.getCalls('load')).toHaveLength(2);
});

it('reloads on a push while idle', async () => {
  const { daemon, hook, bump } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  bump();
  act(() => useDelegationPreferencesPush.getState().push(1));
  await waitFor(() => expect(hook.result.current.preferences?.revision).toBe(1));
  expect(daemon.getCalls('load')).toHaveLength(2);
  expect(daemon.getCalls('save')).toHaveLength(0);
});
