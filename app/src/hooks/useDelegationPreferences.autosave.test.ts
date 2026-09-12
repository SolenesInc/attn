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
  let releaseLoad: (() => void) | null = null;
  let loadFailure = '';
  daemon.setResponse('load', async () => { if (loadFailure) throw new Error(loadFailure); if (releaseLoad) await new Promise<void>(resolve => { const r = releaseLoad; releaseLoad = () => { resolve(); r?.(); }; }); return structuredClone(server); });
  let release: (() => void) | null = null;
  let holding = false;
  const held: (() => void)[] = [];
  daemon.setResponse('save', async (args: unknown[]) => {
    const value = args[0] as DelegationPreferences;
    if (release === null) await new Promise<void>(resolve => { release = resolve; });
    else if (holding) await new Promise<void>(resolve => { held.push(resolve); });
    if (value.revision !== server.preferences.revision) throw new Error('delegation preferences changed; reload before saving or choosing a role');
    server = { ...server, preferences: { ...structuredClone(value), revision: value.revision + 1 } };
    return structuredClone(server);
  });
  const load = daemon.createRequest<DelegationSettingsState>('load');
  const save = daemon.createRequest<DelegationSettingsState>('save');
  const hook = renderHook(() => useDelegationPreferences(true, load, save));
  return { daemon, hook, server: () => server, bump: () => { server = { ...server, preferences: { ...server.preferences, revision: server.preferences.revision + 1 } }; }, releaseFirst: () => { const r = release; release = () => {}; r?.(); }, holdLoads: () => { releaseLoad = () => {}; return () => { const r = releaseLoad; releaseLoad = null; r?.(); }; }, failLoads: (reason: string) => { loadFailure = reason; return () => { loadFailure = ''; }; }, holdSaves: () => { holding = true; return () => { holding = false; held.splice(0).forEach(resolve => resolve()); }; } };
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

it('keeps an install queued behind a running save through the edits that collapse into it', async () => {
  const { daemon, hook, releaseFirst } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  act(() => { void hook.result.current.save(preferences(0, 'one')); });
  act(() => { void hook.result.current.save(preferences(0, 'two'), true); });
  act(() => { void hook.result.current.save(preferences(0, 'three')); });
  releaseFirst();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(daemon.getCalls('save').map(call => [(call.args[0] as DelegationPreferences).fallback.instructions, call.args[1]])).toEqual([['one', false], ['three', true]]);
});

it('keeps the generation when the push announcing its own save reloads the same revision', async () => {
  const { daemon, hook, releaseFirst } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  act(() => { void hook.result.current.save(preferences(0, 'one')); });
  releaseFirst();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(hook.result.current.generation).toBe(1);
  act(() => useDelegationPreferencesPush.getState().push(1));
  await waitFor(() => expect(daemon.getCalls('load')).toHaveLength(2));
  expect(hook.result.current.generation).toBe(1);
});

it('loads again after a save when a newer revision was announced during the flight', async () => {
  const { daemon, hook, releaseFirst } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  act(() => { void hook.result.current.save(preferences(0, 'one')); });
  act(() => useDelegationPreferencesPush.getState().push(2));
  expect(daemon.getCalls('load')).toHaveLength(1);
  releaseFirst();
  await waitFor(() => expect(daemon.getCalls('load')).toHaveLength(2));
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
});

it('discards an edit made while the conflict reload is still loading', async () => {
  const { daemon, hook, server, bump, releaseFirst, holdLoads } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  bump();
  const releaseLoad = holdLoads();
  act(() => { void hook.result.current.save(preferences(0, 'one')); });
  releaseFirst();
  await waitFor(() => expect(hook.result.current.error).toContain('reload before saving'));
  act(() => { void hook.result.current.save(preferences(0, 'two')); });
  releaseLoad();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(daemon.getCalls('save')).toHaveLength(1);
  expect(server().preferences.fallback.instructions).toBe('');
  expect(hook.result.current.preferences?.revision).toBe(1);
  expect(hook.result.current.preferences?.fallback.instructions).toBe('');
  expect(hook.result.current.error).toContain('reload before saving');
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

it('defers a reload asked for during a save until the save drains, so a queued edit is not rolled back or overwritten', async () => {
  const { daemon, hook, server, releaseFirst, holdLoads, holdSaves } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  const releaseLoad = holdLoads();
  const releaseSave = holdSaves();
  act(() => { void hook.result.current.save(preferences(0, 'one')); });
  act(() => { void hook.result.current.reload(); });
  act(() => { void hook.result.current.save(preferences(0, 'two')); });
  releaseFirst();
  await waitFor(() => expect(daemon.getCalls('save')).toHaveLength(2));
  await act(async () => { releaseLoad(); });
  expect(hook.result.current.preferences?.fallback.instructions).toBe('two');
  const current = hook.result.current.preferences!;
  act(() => { void hook.result.current.save({ ...current, fallback: { ...current.fallback, instructions: current.fallback.instructions + '!' } }); });
  releaseSave();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(server().preferences.fallback.instructions).toBe('two!');
  expect(hook.result.current.preferences?.fallback.instructions).toBe('two!');
  expect(daemon.getCalls('load')).toHaveLength(2);
});

it('shows the last confirmed table when a save fails and the recovery load fails too', async () => {
  const { daemon, hook, bump, releaseFirst, failLoads } = setup();
  await waitFor(() => expect(hook.result.current.preferences).not.toBeNull());
  bump();
  const restoreLoads = failLoads('daemon unreachable');
  act(() => { void hook.result.current.save(preferences(0, 'mine')); });
  expect(hook.result.current.preferences?.fallback.instructions).toBe('mine');
  releaseFirst();
  await waitFor(() => expect(hook.result.current.busy).toBe(false));
  expect(daemon.getCalls('load')).toHaveLength(2);
  expect(hook.result.current.error).toBe('daemon unreachable');
  expect(hook.result.current.preferences?.fallback.instructions).toBe('');
  expect(hook.result.current.preferences?.revision).toBe(0);
  restoreLoads();
  await act(async () => { await hook.result.current.reload(); });
  expect(hook.result.current.error).toBe('');
  expect(hook.result.current.preferences?.revision).toBe(1);
});
