import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider, type DaemonApi } from '../contexts/DaemonApiContext';
import { CrewRestartState, type CrewMember } from '../types/generated';
import type { Seed } from '../hooks/useDaemonSocket';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { clearDelegationModelCatalogs } from '../hooks/useDelegationModelCatalog';
import { CrewPanel } from './CrewPanel';

function member(id: string, revision: number, values: Partial<CrewMember> = {}): CrewMember {
  return {
    id,
    revision,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    resolved_agent: 'claude',
    ...values,
  };
}

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '', status: 'planted', state_changed_at: '2026-09-06T16:50:50Z', state_changed_at_exact: true,
    step_slug: overrides.title, planter_session: '', planter_member: '', tender_session: '', tender_member: '',
    edges: [], ready: false, template: false, gate: false, vars: [], rev: 1,
    created_at: '2026-09-06T16:50:50Z', updated_at: '2026-09-06T16:50:50Z', ...overrides,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function api(overrides: Record<string, unknown> = {}): DaemonApi {
  return {
    isConnected: true,
    connectionGeneration: 1,
    sendCrewSet: vi.fn().mockResolvedValue({ success: true, conflict: false }),
    sendCrewRestart: vi.fn().mockResolvedValue({ success: true, conflict: false }),
    sendCrewCharterGet: vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: '# Trellis\n', token: 'charter-1' } }),
    sendCrewCharterSet: vi.fn().mockResolvedValue({ member: 'trellis', conflict: false, charter: { content: '# Trellis\n', token: 'charter-2' } }),
    sendCrewHandoffsGet: vi.fn().mockResolvedValue({ member: 'trellis', handoffs: [] }),
    sendCrewHandoffGet: vi.fn().mockRejectedValue(new Error('no handoff body in this fixture')),
    sendDelegationPreferencesGet: vi.fn().mockResolvedValue({
      preferences: { enabled: false, revision: 0, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
      templates: [],
      harnesses: [
        { id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true },
        { id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true },
        { id: 'fixture', name: 'Fixture', available: false, model_pin: false, effort_pin: false, discovery: false },
      ],
    }),
    sendDelegationModels: vi.fn().mockResolvedValue({
      detail: '',
      models: [
        { harness: 'codex', provider: 'openai', id: 'gpt-6-astra', name: 'Astra', access: 'supported', effort_support: 'supported', effort_levels: ['medium', 'high'] },
        { harness: 'codex', provider: 'openai', id: 'retired', name: 'Retired', access: 'unsupported', effort_support: 'unknown', effort_levels: [] },
      ],
    }),
    ...overrides,
  } as unknown as DaemonApi;
}

function renderPanel({
  daemon = api(),
  members = [member('trellis', 4)],
  sessions = [],
  initialMember,
  isOpen = true,
  onOpenSeed = vi.fn<(seedId: string, placementSessionId?: string) => void>(),
  seeds = [],
}: {
  daemon?: DaemonApi;
  members?: CrewMember[];
  sessions?: any[];
  initialMember?: string;
  isOpen?: boolean;
  onOpenSeed?: ReturnType<typeof vi.fn<(seedId: string, placementSessionId?: string) => void>>;
  seeds?: Seed[];
} = {}) {
  const onClose = vi.fn();
  let visit = 1;
  let open = isOpen;
  const panel = (nextMembers: CrewMember[], nextOpen: boolean) => (
    <DaemonApiProvider api={daemon}>
      <CrewPanel
        visit={visit}
        isOpen={nextOpen}
        initialMember={initialMember}
        members={nextMembers}
        sessions={sessions}
        seeds={seeds}
        seedsTotal={seeds.length}
        onClose={onClose}
        onOpenSeed={onOpenSeed}
      />
    </DaemonApiProvider>
  );
  const view = render(panel(members, isOpen));
  const rerenderPanel = (
    nextMembers: CrewMember[],
    nextOpen = open,
    preserve = false,
  ) => {
    if (nextOpen && !open && !preserve) visit += 1;
    open = nextOpen;
    view.rerender(panel(nextMembers, nextOpen));
  };
  return { ...view, daemon, onClose, onOpenSeed, rerenderPanel };
}

afterEach(() => {
  _resetEscapeStackForTest();
  clearDelegationModelCatalogs();
  vi.restoreAllMocks();
});

describe('CrewPanel', () => {
  it('keeps member, tab, seed filter and search when a workspace seed returns to Crew', async () => {
    const planted = seed({ id: 's-g9yxwv', title: 'Artifact presence comes from the daemon', planter_member: 'keel' });
    const onOpenSeed = vi.fn();
    const members = [member('alder', 2), member('keel', 3, { binding_session: 'session-keel' })];
    const { rerenderPanel } = renderPanel({ members, seeds: [planted], onOpenSeed });

    await waitFor(() => expect(screen.getByLabelText('Harness')).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Seeds' }));
    fireEvent.click(screen.getByRole('button', { name: /Planted/ }));
    fireEvent.change(screen.getByLabelText('Find a seed'), { target: { value: 'Artifact presence' } });
    fireEvent.click(screen.getByRole('button', { name: /Artifact presence comes from the daemon/ }));
    expect(onOpenSeed).toHaveBeenCalledWith(planted.id, 'session-keel');

    await act(async () => {
      rerenderPanel(members, false);
      rerenderPanel(members, true, true);
    });
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Seeds' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('button', { name: /^Planted/ })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText('Find a seed')).toHaveValue('Artifact presence');
  });

  it('opens direct member details on launch settings after returning from a seed', async () => {
    const planted = seed({ id: 's-g9yxwv', title: 'Artifact presence comes from the daemon', planter_member: 'keel' });
    const members = [member('alder', 2), member('keel', 3)];
    const { rerenderPanel } = renderPanel({ initialMember: 'keel', members, seeds: [planted] });

    fireEvent.click(screen.getByRole('button', { name: 'Seeds' }));
    fireEvent.click(screen.getByRole('button', { name: /Planted/ }));
    await act(async () => rerenderPanel(members, false));
    await act(async () => rerenderPanel(members, true));

    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('button', { name: 'Wake' })).toBeInTheDocument();
  });

  it('keeps actual running values separate from acknowledged next-wake settings', async () => {
    renderPanel({
      members: [member('trellis', 4, {
        binding_session: 'session-trellis',
        agent: 'codex', model: 'gpt-6-astra', effort: 'high',
        resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
      })],
      sessions: [{ id: 'session-trellis', agent: 'claude' }],
    });

    const running = await screen.findByLabelText('Running now');
    expect(running).toHaveTextContent('Harnessclaude');
    expect(running).toHaveTextContent('ModelNot reported');
    expect(running).toHaveTextContent('EffortNot reported');
    expect(screen.getByText('Acknowledged next wake').parentElement).toHaveTextContent('codex / gpt-6-astra / high');
    expect(screen.getByLabelText('Harness')).toHaveValue('codex');
  });

  it('saves a full atomic selection, blocks restart until acknowledgment, and clears to defaults', async () => {
    const pending = deferred<any>();
    const sendCrewSet = vi.fn().mockReturnValue(pending.promise);
    const daemon = api({ sendCrewSet });
    renderPanel({ daemon, members: [member('alder', 7, {
      binding_session: 'session-alder', agent: 'codex', model: 'gpt-6-astra', effort: 'high',
      resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
    })] });

    const harness = await screen.findByLabelText('Harness');
    fireEvent.change(harness, { target: { value: '' } });

    expect(sendCrewSet).toHaveBeenCalledWith({
      member: 'alder', expectedRevision: 7, agent: '', model: '', effort: '',
    });
    expect(screen.getByRole('status', { name: '' })).toHaveTextContent('Saving…');
    expect(screen.getByRole('button', { name: 'Handoff and restart' })).toBeDisabled();

    await act(async () => pending.resolve({
      success: true, conflict: false,
      member: member('alder', 8, { binding_session: 'session-alder', resolved_agent: 'claude' }),
    }));
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Handoff and restart' })).toBeEnabled();
    expect(harness).toHaveValue('');
  });

  it('keeps a failed member edit through roster navigation and retries it', async () => {
    const retry = deferred<any>();
    const sendCrewSet = vi.fn()
      .mockResolvedValueOnce({ success: false, conflict: false, error: 'model discovery is unavailable' })
      .mockReturnValueOnce(retry.promise);
    renderPanel({ daemon: api({ sendCrewSet }), members: [member('alder', 2), member('keel', 3)] });

    const effort = await screen.findByLabelText('Reasoning effort');
    fireEvent.change(screen.getByLabelText('Harness'), { target: { value: 'codex' } });
    await waitFor(() => expect(screen.getByText('Not saved')).toBeInTheDocument());
    expect(screen.getByText('model discovery is unavailable')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: /Alder/ }));
    expect(screen.getByLabelText('Harness')).toHaveValue('codex');
    expect(effort).toHaveValue('');

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(sendCrewSet).toHaveBeenCalledTimes(2);
    await act(async () => retry.resolve({
      success: true, conflict: false,
      member: member('alder', 3, { agent: 'codex', resolved_agent: 'codex' }),
    }));
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument());
  });

  it('retries an unacknowledged restart with the same identity and original guards', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111');
    const sendCrewRestart = vi.fn()
      .mockRejectedValueOnce(new Error('Restarting Trellis timed out'))
      .mockResolvedValueOnce({
        success: true, conflict: false,
        member: member('trellis', 10, {
          binding_session: 'session-trellis', resolved_agent: 'claude',
          restart: { request_id: '11111111-1111-4111-8111-111111111111', session_id: 'session-trellis', state: CrewRestartState.Queued },
        }),
      });
    renderPanel({ daemon: api({ sendCrewRestart }), members: [member('trellis', 9, {
      binding_session: 'session-trellis', resolved_agent: 'claude',
    })] });

    fireEvent.click(await screen.findByRole('button', { name: 'Handoff and restart' }));
    fireEvent.click(screen.getByRole('button', { name: 'Request handoff and restart' }));
    await screen.findByText('Restarting Trellis timed out');
    fireEvent.click(screen.getByRole('button', { name: 'Retry delivery' }));

    await waitFor(() => expect(sendCrewRestart).toHaveBeenCalledTimes(2));
    expect(sendCrewRestart.mock.calls[0][0]).toEqual(sendCrewRestart.mock.calls[1][0]);
    expect(sendCrewRestart).toHaveBeenCalledWith({
      member: 'trellis',
      requestId: '11111111-1111-4111-8111-111111111111',
      expectedSessionId: 'session-trellis',
      expectedRevision: 9,
    });
  });

  it('keeps a redelivered restart result when the superseded delivery settles late', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('22222222-2222-4222-8222-222222222222');
    const first = deferred<any>();
    const sendCrewRestart = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({
        success: true, conflict: false,
        member: member('trellis', 10, {
          binding_session: 'session-trellis', resolved_agent: 'claude',
          restart: { request_id: '22222222-2222-4222-8222-222222222222', session_id: 'session-trellis', state: CrewRestartState.Queued },
        }),
      });
    renderPanel({ daemon: api({ sendCrewRestart }), members: [member('trellis', 9, {
      binding_session: 'session-trellis', resolved_agent: 'claude',
    })] });

    fireEvent.click(await screen.findByRole('button', { name: 'Handoff and restart' }));
    fireEvent.click(screen.getByRole('button', { name: 'Request handoff and restart' }));
    await act(async () => first.reject(new Error('Restarting Trellis timed out')));
    fireEvent.click(await screen.findByRole('button', { name: 'Retry delivery' }));
    await waitFor(() => expect(sendCrewRestart).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Retry delivery' })).not.toBeInTheDocument());
  });

  it('keeps the panel behind the restart confirmation out of the tab order', async () => {
    renderPanel({ members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })] });
    fireEvent.click(await screen.findByRole('button', { name: 'Handoff and restart' }));
    const dialog = screen.getByRole('alertdialog');
    expect(screen.getByTestId('crew-panel-close').closest('[inert]')).not.toBeNull();
    expect(screen.getByLabelText('Crew roster').closest('[inert]')).not.toBeNull();
    expect(dialog.closest('[inert]')).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.getByTestId('crew-panel-close').closest('[inert]')).toBeNull();
  });

  it.each([
    [CrewRestartState.Completed, 'New day started'],
    [CrewRestartState.Failed, 'Successor launch failed'],
  ])('reconciles a lost restart response with authoritative %s state', async (state, copy) => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('22222222-2222-4222-8222-222222222222');
    const sendCrewRestart = vi.fn().mockRejectedValue(new Error('Restart response was lost'));
    const daemon = api({ sendCrewRestart });
    const initial = member('trellis', 9, {
      binding_session: 'session-trellis', resolved_agent: 'claude',
    });
    const { rerenderPanel } = renderPanel({ daemon, members: [initial] });

    fireEvent.click(await screen.findByRole('button', { name: 'Handoff and restart' }));
    fireEvent.click(screen.getByRole('button', { name: 'Request handoff and restart' }));
    await screen.findByText('Restart response was lost');

    rerenderPanel([member('trellis', 10, {
      binding_session: state === CrewRestartState.Completed ? 'successor-session' : 'session-trellis',
      resolved_agent: 'claude',
      restart: {
        request_id: '22222222-2222-4222-8222-222222222222',
        session_id: 'session-trellis',
        state,
        ...(state === CrewRestartState.Completed
          ? { successor_session_id: 'successor-session' }
          : { error: 'Successor launch failed' }),
      },
    })]);

    await screen.findByText(new RegExp(copy));
    expect(screen.queryByRole('button', { name: 'Retry delivery' })).not.toBeInTheDocument();
  });

  it('shows a newer authoritative restart after the local attempt completed', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('33333333-3333-4333-8333-333333333333');
    const localCompleted = member('trellis', 10, {
      binding_session: 'successor-session',
      resolved_agent: 'claude',
      restart: {
        request_id: '33333333-3333-4333-8333-333333333333',
        session_id: 'session-trellis',
        state: CrewRestartState.Completed,
        successor_session_id: 'successor-session',
      },
    });
    const sendCrewRestart = vi.fn().mockResolvedValue({
      success: true, conflict: false, member: localCompleted,
    });
    const { rerenderPanel } = renderPanel({ daemon: api({ sendCrewRestart }), members: [member('trellis', 9, {
      binding_session: 'session-trellis', resolved_agent: 'claude',
    })] });

    fireEvent.click(await screen.findByRole('button', { name: 'Handoff and restart' }));
    fireEvent.click(screen.getByRole('button', { name: 'Request handoff and restart' }));
    await screen.findByText(/New day started/);

    rerenderPanel([member('trellis', 11, {
      binding_session: 'successor-session',
      resolved_agent: 'claude',
      restart: {
        request_id: '44444444-4444-4444-8444-444444444444',
        session_id: 'successor-session',
        state: CrewRestartState.Requested,
      },
    })]);

    await screen.findByText('Handoff requested');
    expect(screen.queryByText(/New day started/)).not.toBeInTheDocument();
  });

  it('closes with Escape and wakes an asleep member through the guarded restart action', async () => {
    const sendCrewRestart = vi.fn().mockResolvedValue({ success: true, conflict: false });
    const { onClose } = renderPanel({ daemon: api({ sendCrewRestart }), members: [member('keel', 5)] });

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(await screen.findByRole('button', { name: 'Wake' }));
    const dialog = screen.getByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Wake member' }));
    await waitFor(() => expect(sendCrewRestart).toHaveBeenCalledWith(expect.objectContaining({
      member: 'keel', expectedSessionId: '', expectedRevision: 5,
    })));
  });

  it('keeps roster navigation while async harness discovery settles', async () => {
    const preferences = deferred<any>();
    const daemon = api({ sendDelegationPreferencesGet: vi.fn().mockReturnValue(preferences.promise) });
    renderPanel({ daemon, initialMember: 'alder', members: [member('alder', 2), member('keel', 3)] });

    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();

    await act(async () => preferences.resolve({
      preferences: { enabled: false, revision: 0, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
      templates: [],
      harnesses: [{ id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true }],
    }));
    await waitFor(() => expect(screen.getByLabelText('Harness')).toBeEnabled());
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(daemon.sendDelegationPreferencesGet).toHaveBeenCalledTimes(1);
  });

  it('allows model and effort pins while the harness follows the crew default', async () => {
    const sendCrewSet = vi.fn()
      .mockResolvedValueOnce({
        success: true,
        conflict: false,
        member: member('keel', 6, {
          model: 'openai/gpt-6-astra',
          resolved_agent: 'codex', resolved_model: 'openai/gpt-6-astra',
        }),
      })
      .mockResolvedValueOnce({
        success: true,
        conflict: false,
        member: member('keel', 7, {
          model: 'openai/gpt-6-astra', effort: 'high',
          resolved_agent: 'codex', resolved_model: 'openai/gpt-6-astra', resolved_effort: 'high',
        }),
      });
    renderPanel({ daemon: api({ sendCrewSet }), members: [member('keel', 5, { resolved_agent: 'codex' })] });

    const model = await screen.findByLabelText('Model');
    await waitFor(() => expect(model).toBeEnabled());
    await waitFor(() => expect(screen.getByRole('option', { name: 'openai / Astra' })).toBeInTheDocument());
    fireEvent.change(model, { target: { value: 'openai/gpt-6-astra' } });
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument());
    const effort = screen.getByLabelText('Reasoning effort');
    fireEvent.change(effort, { target: { value: 'hig' } });
    fireEvent.change(effort, { target: { value: 'high' } });
    expect(sendCrewSet).toHaveBeenCalledTimes(1);
    fireEvent.blur(effort);

    await waitFor(() => expect(sendCrewSet).toHaveBeenLastCalledWith({
      member: 'keel', expectedRevision: 6, agent: '', model: 'openai/gpt-6-astra', effort: 'high',
    }));
    expect(sendCrewSet).toHaveBeenCalledTimes(2);
  });

  it('commits a typed model id on Enter as one write and keeps the draft over roster pushes', async () => {
    const sendCrewSet = vi.fn().mockResolvedValue({ success: true, conflict: false });
    const members = [member('keel', 6)];
    const { rerenderPanel } = renderPanel({ daemon: api({ sendCrewSet }), members });
    const model = await screen.findByLabelText('Model');
    await waitFor(() => expect(model).toBeEnabled());
    fireEvent.change(model, { target: { value: '__custom' } });
    const custom = screen.getByTestId('crew-custom-model');
    fireEvent.change(custom, { target: { value: 'gpt-7' } });
    await act(async () => { rerenderPanel([member('keel', 7)]); });
    expect(custom).toHaveValue('gpt-7');
    expect(sendCrewSet).not.toHaveBeenCalled();
    fireEvent.keyDown(custom, { key: 'Enter' });
    await waitFor(() => expect(sendCrewSet).toHaveBeenCalledExactlyOnceWith({
      member: 'keel', expectedRevision: 7, agent: '', model: 'gpt-7', effort: '',
    }));
  });

  it('keeps dependent controls pending while an explicit harness pin clears', async () => {
    const pending = deferred<any>();
    const daemon = api({ sendCrewSet: vi.fn().mockReturnValue(pending.promise) });
    renderPanel({ daemon, members: [member('keel', 5, {
      agent: 'codex', resolved_agent: 'codex', model: 'openai/gpt-6-astra', resolved_model: 'openai/gpt-6-astra',
    })] });

    const model = await screen.findByLabelText('Model');
    await waitFor(() => expect(model).toBeEnabled());
    fireEvent.change(screen.getByLabelText('Harness'), { target: { value: '' } });

    expect(model).toBeDisabled();
    expect(screen.getByLabelText('Reasoning effort')).toBeDisabled();

    await act(async () => pending.resolve({
      success: true,
      conflict: false,
      member: member('keel', 6, { resolved_agent: 'claude' }),
    }));
    await waitFor(() => expect(model).toBeEnabled());
  });

  it('selects provider-qualified model identities when bare ids collide', async () => {
    const sendCrewSet = vi.fn().mockResolvedValue({
      success: true,
      conflict: false,
      member: member('keel', 6, {
        agent: 'codex', model: 'second/shared', resolved_agent: 'codex', resolved_model: 'second/shared',
      }),
    });
    const sendDelegationModels = vi.fn().mockResolvedValue({
      detail: '',
      models: [
        { harness: 'codex', provider: 'first', id: 'shared', name: 'Shared one', access: 'supported', effort_support: 'supported', effort_levels: ['low'] },
        { harness: 'codex', provider: 'second', id: 'shared', name: 'Shared two', access: 'supported', effort_support: 'supported', effort_levels: ['high'] },
      ],
    });
    renderPanel({ daemon: api({ sendCrewSet, sendDelegationModels }), members: [member('keel', 5, {
      agent: 'codex', resolved_agent: 'codex',
    })] });

    const model = await screen.findByLabelText('Model');
    await screen.findByRole('option', { name: 'second / Shared two' });
    fireEvent.change(model, { target: { value: 'second/shared' } });

    await waitFor(() => expect(sendCrewSet).toHaveBeenCalledWith({
      member: 'keel', expectedRevision: 5, agent: 'codex', model: 'second/shared', effort: '',
    }));
    expect(model).toHaveValue('second/shared');
  });

  it('keeps an unsupported models explicit effort clear through a concurrent update', async () => {
    const retry = deferred<any>();
    const sendCrewSet = vi.fn()
      .mockResolvedValueOnce({
        success: false,
        conflict: true,
        error: 'revision conflict',
        member: member('keel', 6, {
          agent: 'codex', effort: 'high', resolved_agent: 'codex', resolved_effort: 'high',
        }),
      })
      .mockReturnValueOnce(retry.promise);
    const sendDelegationModels = vi.fn().mockResolvedValue({
      detail: '',
      models: [{
        harness: 'codex', provider: 'local', id: 'fixed', name: 'Fixed', access: 'supported',
        effort_support: 'unsupported', effort_levels: [],
      }],
    });
    renderPanel({ daemon: api({ sendCrewSet, sendDelegationModels }), members: [member('keel', 5, {
      agent: 'codex', resolved_agent: 'codex',
    })] });

    const model = await screen.findByLabelText('Model');
    await screen.findByRole('option', { name: 'local / Fixed' });
    fireEvent.change(model, { target: { value: 'local/fixed' } });
    await screen.findByText('Not saved');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    await waitFor(() => expect(sendCrewSet).toHaveBeenLastCalledWith({
      member: 'keel', expectedRevision: 6, agent: 'codex', model: 'local/fixed', effort: '',
    }));
    await act(async () => retry.resolve({
      success: true,
      conflict: false,
      member: member('keel', 7, {
        agent: 'codex', model: 'local/fixed', resolved_agent: 'codex', resolved_model: 'local/fixed',
      }),
    }));
    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument());
  });

  it('loads the full charter on demand and flushes it before tab navigation', async () => {
    const save = deferred<any>();
    const sendCrewCharterGet = vi.fn().mockResolvedValue({
      member: 'trellis',
      charter: { content: '# Trellis\n\nFull **Markdown** charter.\n', token: 'charter-old' },
    });
    const sendCrewCharterSet = vi.fn().mockReturnValue(save.promise);
    const sendCrewHandoffsGet = vi.fn().mockResolvedValue({ member: 'trellis', handoffs: [] });
    renderPanel({ daemon: api({ sendCrewCharterGet, sendCrewCharterSet, sendCrewHandoffsGet }) });

    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    expect(editor).toHaveValue('# Trellis\n\nFull **Markdown** charter.\n');
    fireEvent.change(editor, { target: { value: '# Trellis\n\nChanged while the idea is hot.\n' } });
    expect(screen.getByRole('status')).toHaveTextContent('Waiting to save');

    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    expect(screen.getByTestId('crew-charter-editor')).toBeInTheDocument();
    await waitFor(() => {
      expect(sendCrewCharterSet).toHaveBeenCalledWith(
        'trellis', '# Trellis\n\nChanged while the idea is hot.\n', 'charter-old',
      );
      expect(screen.getByRole('status')).toHaveTextContent('Saving');
    });

    await act(async () => save.resolve({
      member: 'trellis', conflict: false,
      charter: { content: '# Trellis\n\nChanged while the idea is hot.\n', token: 'charter-new' },
    }));
    await screen.findByText('No handoffs recorded.');
    expect(sendCrewHandoffsGet).toHaveBeenCalledWith('trellis');
  });

  it('uses only the latest navigation intent while one charter flush is pending', async () => {
    const save = deferred<any>();
    const sendCrewCharterSet = vi.fn().mockReturnValue(save.promise);
    const sendCrewHandoffsGet = vi.fn().mockResolvedValue({ member: 'trellis', handoffs: [] });
    renderPanel({
      daemon: api({
        sendCrewCharterGet: vi.fn().mockResolvedValue({
          member: 'trellis', charter: { content: 'old', token: 'old-token' },
        }),
        sendCrewCharterSet,
        sendCrewHandoffsGet,
      }),
      members: [member('trellis', 4), member('keel', 5)],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'new' } });

    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    await waitFor(() => expect(sendCrewCharterSet).toHaveBeenCalledTimes(1));
    await act(async () => save.resolve({
      member: 'trellis', conflict: false, charter: { content: 'new', token: 'new-token' },
    }));

    expect(await screen.findByText('No handoffs recorded.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Trellis' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');
    expect(sendCrewHandoffsGet).toHaveBeenCalledTimes(1);
  });

  it('preserves the selected member and tab when returning from a workspace seed', async () => {
    const members = [member('alder', 2), member('trellis', 3)];
    const { rerenderPanel } = renderPanel({ members });
    fireEvent.click(screen.getByRole('button', { name: /Trellis/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    await screen.findByText('No handoffs recorded.');

    await act(async () => { rerenderPanel(members, false); });
    await act(async () => { rerenderPanel(members, true, true); });

    expect(screen.getByRole('heading', { name: 'Trellis' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');
  });

  it('returns to launch settings on a normal reopen', async () => {
    const members = [member('alder', 2), member('trellis', 3)];
    const { rerenderPanel } = renderPanel({ members, initialMember: 'alder' });
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    expect(screen.getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');

    await act(async () => { rerenderPanel(members, false); });
    await act(async () => { rerenderPanel(members, true); });

    expect(screen.getByRole('heading', { name: 'Alder' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
  });

  it('flushes a charter before closing the panel', async () => {
    const save = deferred<any>();
    const { onClose } = renderPanel({ daemon: api({
      sendCrewCharterGet: vi.fn().mockResolvedValue({
        member: 'trellis', charter: { content: 'old', token: 'old-token' },
      }),
      sendCrewCharterSet: vi.fn().mockReturnValue(save.promise),
    }) });
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'new' } });
    fireEvent.click(screen.getByTestId('crew-panel-close'));
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => save.resolve({
      member: 'trellis', conflict: false, charter: { content: 'new', token: 'new-token' },
    }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it('keeps a failed navigation flush visible and retries the retained edit', async () => {
    const sendCrewCharterSet = vi.fn()
      .mockRejectedValueOnce(new Error('disk is read-only'))
      .mockResolvedValueOnce({ member: 'trellis', conflict: false, charter: { content: 'local edit', token: 'new' } });
    renderPanel({ daemon: api({
      sendCrewCharterGet: vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: 'old', token: 'old' } }),
      sendCrewCharterSet,
    }) });
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'local edit' } });
    fireEvent.click(screen.getByRole('button', { name: 'Launch settings' }));

    await screen.findByText('disk is read-only');
    expect(editor).toHaveValue('local edit');
    expect(screen.getByRole('button', { name: 'Charter' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Saved'));
    fireEvent.click(screen.getByRole('button', { name: 'Launch settings' }));
    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
  });

  it('lets the user leave after a charter save failure has been shown, keeping the edit for later', async () => {
    const sendCrewCharterSet = vi.fn().mockRejectedValue(new Error('WebSocket not connected'));
    renderPanel({ daemon: api({
      sendCrewCharterGet: vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: 'old', token: 'old' } }),
      sendCrewCharterSet,
    }) });
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'offline edit' } });
    fireEvent.click(screen.getByRole('button', { name: 'Launch settings' }));
    await screen.findByText('WebSocket not connected');
    expect(screen.getByRole('button', { name: 'Charter' })).toHaveAttribute('aria-current', 'page');

    fireEvent.click(screen.getByRole('button', { name: 'Launch settings' }));
    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    expect(screen.getByTestId('crew-charter-editor')).toHaveValue('offline edit');
    expect(screen.getByText('The charter was not saved. Your edit is still here.')).toBeInTheDocument();
    expect(sendCrewCharterSet).toHaveBeenCalledTimes(1);
  });

  it('keeps a retained offline charter edit across close and a fresh reopen', async () => {
    const members = [member('trellis', 4)];
    const sendCrewCharterSet = vi.fn().mockRejectedValue(new Error('WebSocket not connected'));
    const { onClose, rerenderPanel } = renderPanel({ members, daemon: api({
      sendCrewCharterGet: vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: 'old', token: 'old' } }),
      sendCrewCharterSet,
    }) });
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'offline edit' } });
    fireEvent.click(screen.getByRole('button', { name: 'Launch settings' }));
    await screen.findByText('WebSocket not connected');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    rerenderPanel(members, false);
    rerenderPanel(members, true);

    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    expect(await screen.findByTestId('crew-charter-editor')).toHaveValue('offline edit');
    expect(screen.getByTestId('crew-charter-status')).not.toHaveTextContent('Saved');
  });

  it('commits a launch field that still has focus when Escape closes the panel', async () => {
    const sendCrewSet = vi.fn().mockResolvedValue({ success: true, conflict: false });
    const { onClose, rerenderPanel } = renderPanel({ daemon: api({ sendCrewSet }), members: [member('keel', 6)] });
    const effort = await screen.findByLabelText('Reasoning effort');
    effort.focus();
    fireEvent.change(effort, { target: { value: 'high' } });
    expect(sendCrewSet).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    rerenderPanel([member('keel', 6)], false);

    await waitFor(() => expect(sendCrewSet).toHaveBeenCalledWith(expect.objectContaining({ member: 'keel', effort: 'high' })));
    expect(sendCrewSet).toHaveBeenCalledTimes(1);
  });

  it('renders no roster or member content while closed', () => {
    renderPanel({ isOpen: false });
    expect(screen.queryByRole('region', { name: 'Crew roster' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Crew roster')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Harness')).not.toBeInTheDocument();
  });

  it('returns the authoritative charter on conflict and requires an explicit choice', async () => {
    const sendCrewCharterSet = vi.fn().mockResolvedValue({
      member: 'trellis', conflict: true, charter: { content: 'external edit', token: 'external' },
    });
    renderPanel({ daemon: api({
      sendCrewCharterGet: vi.fn().mockResolvedValue({ member: 'trellis', charter: { content: 'old', token: 'old' } }),
      sendCrewCharterSet,
    }) });
    fireEvent.click(screen.getByRole('button', { name: 'Charter' }));
    const editor = await screen.findByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'my edit' } });
    fireEvent.blur(editor);
    await screen.findByText('The file changed outside this editor.');
    expect(editor).toHaveValue('my edit');
    fireEvent.click(screen.getByRole('button', { name: 'Use file version' }));
    expect(editor).toHaveValue('external edit');
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
  });

  it('renders complete dated handoffs and opens seed links through the panel callback', async () => {
    const onOpenSeed = vi.fn();
    const body = '# Full handoff\n\nA paragraph at the end that must not be truncated.\n\n[Open the seed](s-work11)\n';
    const sendCrewHandoffGet = vi.fn().mockResolvedValue({
      member: 'trellis',
      handoff: { filename: '2026-09-01T21-37Z-trellis.md', occurred_at: '2026-09-01T21:37:00Z', content: body, token: 'letter' },
    });
    renderPanel({
      onOpenSeed,
      daemon: api({
        sendCrewHandoffsGet: vi.fn().mockResolvedValue({
          member: 'trellis',
          handoffs: [{ filename: '2026-09-01T21-37Z-trellis.md', occurred_at: '2026-09-01T21:37:00Z' }],
        }),
        sendCrewHandoffGet,
      }),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    expect(await screen.findByRole('heading', { name: 'Full handoff' })).toBeInTheDocument();
    expect(sendCrewHandoffGet).toHaveBeenCalledExactlyOnceWith('trellis', '2026-09-01T21-37Z-trellis.md');
    expect(screen.getByText('A paragraph at the end that must not be truncated.')).toBeInTheDocument();
    expect(screen.getAllByText(/Sep 1, 2026/)).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: 'Open the seed' }));
    expect(onOpenSeed).toHaveBeenCalledWith('s-work11', undefined);
  });

  it('places a handoff seed beside the selected member current day', async () => {
    const onOpenSeed = vi.fn();
    renderPanel({
      onOpenSeed,
      members: [member('trellis', 4, { binding_session: 'session-trellis' })],
      daemon: api({
        sendCrewHandoffsGet: vi.fn().mockResolvedValue({
          member: 'trellis',
          handoffs: [{ filename: '2026-09-01T21-37Z-trellis.md', occurred_at: '2026-09-01T21:37:00Z' }],
        }),
        sendCrewHandoffGet: vi.fn().mockResolvedValue({
          member: 'trellis',
          handoff: {
            filename: '2026-09-01T21-37Z-trellis.md',
            occurred_at: '2026-09-01T21:37:00Z',
            content: '[Open the seed](s-work11)',
            token: 'letter',
          },
        }),
      }),
    });
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Open the seed' }));
    expect(onOpenSeed).toHaveBeenCalledWith('s-work11', 'session-trellis');
  });

  it('shows one handoff read failure and retries to an honest empty history', async () => {
    const sendCrewHandoffsGet = vi.fn()
      .mockRejectedValueOnce(new Error('handoffs are temporarily unavailable'))
      .mockResolvedValueOnce({ member: 'keel', handoffs: [] });
    renderPanel({
      daemon: api({ sendCrewHandoffsGet }),
      members: [member('keel', 5)],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    expect(await screen.findByText('handoffs are temporarily unavailable')).toBeInTheDocument();
    expect(sendCrewHandoffsGet).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('No handoffs recorded.')).toBeInTheDocument();
    expect(sendCrewHandoffsGet).toHaveBeenCalledTimes(2);
  });

  it('keeps a newer reconnect handoff result when the older read arrives last', async () => {
    const older = deferred<any>();
    const newer = deferred<any>();
    const sendCrewHandoffsGet = vi.fn()
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    const sendCrewHandoffGet = vi.fn().mockResolvedValue({
      member: 'trellis',
      handoff: { filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z', content: '# Newer reconnect result\n', token: 'newer' },
    });
    const daemon = api({ sendCrewHandoffsGet, sendCrewHandoffGet });
    const members = [member('trellis', 4)];
    const { rerenderPanel } = renderPanel({ daemon, members });
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    await waitFor(() => expect(sendCrewHandoffsGet).toHaveBeenCalledTimes(1));

    (daemon as any).connectionGeneration = 2;
    await act(async () => { rerenderPanel(members); });
    await waitFor(() => expect(sendCrewHandoffsGet).toHaveBeenCalledTimes(2));
    await act(async () => newer.resolve({
      member: 'trellis',
      handoffs: [{ filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z' }],
    }));
    expect(await screen.findByRole('heading', { name: 'Newer reconnect result' })).toBeInTheDocument();

    await act(async () => older.resolve({
      member: 'trellis',
      handoffs: [{ filename: '2026-09-01T09-00Z-trellis.md', occurred_at: '2026-09-01T09:00:00Z' }],
    }));
    expect(screen.getByRole('heading', { name: 'Newer reconnect result' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Obsolete delayed result' })).not.toBeInTheDocument();
    expect(sendCrewHandoffGet).toHaveBeenCalledExactlyOnceWith('trellis', '2026-09-02T09-00Z-trellis.md');
  });

  it('reads one letter at a time and retries a failed letter without reloading the history', async () => {
    const sendCrewHandoffsGet = vi.fn().mockResolvedValue({
      member: 'trellis',
      handoffs: [
        { filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z' },
        { filename: '2026-09-01T09-00Z-trellis.md', occurred_at: '2026-09-01T09:00:00Z' },
      ],
    });
    const sendCrewHandoffGet = vi.fn()
      .mockResolvedValueOnce({ member: 'trellis', handoff: { filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z', content: '# Latest letter\n', token: 'a' } })
      .mockRejectedValueOnce(new Error('the older letter is unreadable'))
      .mockResolvedValueOnce({ member: 'trellis', handoff: { filename: '2026-09-01T09-00Z-trellis.md', occurred_at: '2026-09-01T09:00:00Z', content: '# Older letter\n', token: 'b' } })
      .mockResolvedValue({ member: 'trellis', handoff: { filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z', content: '# Latest letter\n', token: 'a' } });
    renderPanel({ daemon: api({ sendCrewHandoffsGet, sendCrewHandoffGet }) });
    fireEvent.click(screen.getByRole('button', { name: 'Handoffs' }));
    expect(await screen.findByRole('heading', { name: 'Latest letter' })).toBeInTheDocument();
    expect(sendCrewHandoffGet).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('crew-handoff-1'));
    expect(await screen.findByText('the older letter is unreadable')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Latest letter' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('crew-handoff-letter-retry'));
    expect(await screen.findByRole('heading', { name: 'Older letter' })).toBeInTheDocument();
    expect(sendCrewHandoffGet).toHaveBeenCalledTimes(3);
    expect(sendCrewHandoffsGet).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('crew-handoff-0'));
    expect(await screen.findByRole('heading', { name: 'Latest letter' })).toBeInTheDocument();
    expect(sendCrewHandoffGet).toHaveBeenCalledTimes(4);
  });
});
