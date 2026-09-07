import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider, type DaemonApi } from '../contexts/DaemonApiContext';
import { CrewRestartState, type CrewMember } from '../types/generated';
import type { Seed } from '../hooks/useDaemonSocket';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
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
  seeds = [],
  onOpenSeed = vi.fn() as (seedId: string, placementSessionId?: string) => void,
}: {
  daemon?: DaemonApi;
  members?: CrewMember[];
  sessions?: any[];
  initialMember?: string;
  seeds?: Seed[];
  onOpenSeed?: (seedId: string, placementSessionId?: string) => void;
} = {}) {
  const onClose = vi.fn();
  const view = render(
    <DaemonApiProvider api={daemon}>
      <CrewPanel
        isOpen
        initialMember={initialMember}
        members={members}
        sessions={sessions}
        seeds={seeds}
        seedsTotal={seeds.length}
        onOpenSeed={onOpenSeed}
        onClose={onClose}
      />
    </DaemonApiProvider>,
  );
  const rerenderPanel = (nextMembers: CrewMember[], isOpen = true, preserveStateOnOpen = false) => view.rerender(
    <DaemonApiProvider api={daemon}>
      <CrewPanel
        isOpen={isOpen}
        initialMember={initialMember}
        members={nextMembers}
        sessions={sessions}
        seeds={seeds}
        seedsTotal={seeds.length}
        preserveStateOnOpen={preserveStateOnOpen}
        onOpenSeed={onOpenSeed}
        onClose={onClose}
      />
    </DaemonApiProvider>,
  );
  return { ...view, daemon, onClose, onOpenSeed, rerenderPanel };
}

afterEach(() => {
  _resetEscapeStackForTest();
  vi.restoreAllMocks();
});

describe('CrewPanel', () => {
  it('keeps member, tab and seed filter when a workspace seed returns to Crew', async () => {
    const planted = seed({ id: 's-g9yxwv', title: 'Artifact presence comes from the daemon', planter_member: 'keel' });
    const onOpenSeed = vi.fn();
    const members = [member('alder', 2), member('keel', 3, { binding_session: 'session-keel' })];
    const { rerenderPanel } = renderPanel({ members, seeds: [planted], onOpenSeed });

    await waitFor(() => expect(screen.getByLabelText('Harness')).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Seeds' }));
    fireEvent.click(screen.getByRole('button', { name: /Planted/ }));
    fireEvent.click(screen.getByRole('button', { name: /Artifact presence comes from the daemon/ }));
    expect(onOpenSeed).toHaveBeenCalledWith(planted.id, 'session-keel');

    await act(async () => {
      rerenderPanel(members, false);
      rerenderPanel(members, true, true);
    });
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Seeds' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('button', { name: /^Planted/ })).toHaveAttribute('aria-pressed', 'true');
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
    fireEvent.change(screen.getByLabelText('Reasoning effort'), { target: { value: 'high' } });

    await waitFor(() => expect(sendCrewSet).toHaveBeenLastCalledWith({
      member: 'keel', expectedRevision: 6, agent: '', model: 'openai/gpt-6-astra', effort: 'high',
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
});
