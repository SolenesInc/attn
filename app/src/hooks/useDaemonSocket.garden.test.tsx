import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  sent: string[] = [];

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.(new Event('open'));
    });
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

async function waitForOpenSocket(): Promise<FakeWebSocket> {
  await waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  });
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  });
  return ws;
}

function seed(id: string, title: string) {
  return {
    id,
    title,
    body: '',
    status: 'planted',
    step_slug: title,
    workspace_id: 'ws-1',
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: '2026-08-12T10:00:00Z',
    updated_at: '2026-08-12T10:00:00Z',
  };
}

describe('useDaemonSocket garden', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  async function renderWithGarden(initialSeeds?: unknown[]) {
    const onSeedsUpdate = vi.fn();
    const hook = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onSeedsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );
    const ws = await waitForOpenSocket();
    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: PROTOCOL_VERSION,
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
        ...(initialSeeds ? { seeds: initialSeeds } : {}),
      });
    });
    return { ws, onSeedsUpdate, hook };
  }

  function reviewItem(overrides: Record<string, unknown> = {}) {
    return {
      id: 'r-1.s-review1',
      run_id: 'r-1',
      seed_id: 's-review1',
      seed_rev: 4,
      evidence_version: 'evidence-1',
      title: 'Review this seed',
      body: 'Check the outcome.',
      evidence: [{ label: 'Review signal', text: 'The seed is stale.' }],
      actions: ['handover', 'park', 'harvest', 'wither'],
      status: 'ready',
      resolution: 'unresolved',
      recommendation: 'park',
      explanation: 'Useful work remains, but it does not need an agent now.',
      ...overrides,
    };
  }

  function review(items = [reviewItem()], runOverrides: Record<string, unknown> = {}) {
    return {
      run: {
        id: 'r-1', candidate_ids: items.map((item) => item.seed_id),
        recipe: { agent: 'codex', model: 'gpt-5.6-luna', effort: 'xhigh' },
        status: 'running', captured_at: '2026-08-30T10:00:00Z',
        ...runOverrides,
      },
      items,
    };
  }

  it('seeds the garden from initial_state', async () => {
    const { onSeedsUpdate } = await renderWithGarden([seed('s-aaa111', 'already planted')]);

    expect(onSeedsUpdate).toHaveBeenCalledWith(
      [expect.objectContaining({ id: 's-aaa111', title: 'already planted' })],
      1,
    );
  });

  it('replaces the garden on every planting broadcast', async () => {
    const { ws, onSeedsUpdate } = await renderWithGarden([seed('s-aaa111', 'already planted')]);

    act(() => {
      ws.emit({
        event: 'garden_seeds_updated',
        seeds: [seed('s-bbb222', 'just planted'), seed('s-aaa111', 'already planted')],
        total: 2,
      });
    });

    expect(onSeedsUpdate).toHaveBeenLastCalledWith(
      [expect.objectContaining({ id: 's-bbb222' }), expect.objectContaining({ id: 's-aaa111' })],
      2,
    );
  });

  it('reads a garden-less daemon as an empty garden', async () => {
    const { onSeedsUpdate } = await renderWithGarden();

    expect(onSeedsUpdate).toHaveBeenCalledWith([], 0);
  });

  it('carries how many seeds the garden holds, not just the ones it sent', async () => {
    const { ws, onSeedsUpdate } = await renderWithGarden();

    act(() => {
      ws.emit({
        event: 'garden_seeds_updated',
        seeds: [seed('s-bbb222', 'the newest one')],
        total: 1421,
      });
    });

    expect(onSeedsUpdate).toHaveBeenLastCalledWith(
      [expect.objectContaining({ id: 's-bbb222' })],
      1421,
    );
  });

  it('reads a total-less push as exactly what it sent', async () => {
    const { ws, onSeedsUpdate } = await renderWithGarden();

    act(() => {
      ws.emit({ event: 'garden_seeds_updated', seeds: [seed('s-ccc333', 'no total here')] });
    });

    expect(onSeedsUpdate).toHaveBeenLastCalledWith(
      [expect.objectContaining({ id: 's-ccc333' })],
      1,
    );
  });

  it('correlates the Review garden overview and keeps progressive updates', async () => {
    const { ws, hook } = await renderWithGarden();
    let pending!: Promise<unknown>;
    act(() => {
      pending = hook.result.current.sendSeedReviewShow();
    });
    const command = ws.sent.map((raw) => JSON.parse(raw)).find((message) => message.cmd === 'seed_review_show');
    expect(command).toBeTruthy();

    act(() => {
      ws.emit({
        event: 'seed_review_result', request_id: command.request_id, operation: 'show',
        success: true, candidate_count: 1, review: review(),
      });
    });
    await expect(pending).resolves.toEqual(expect.objectContaining({ candidateCount: 1 }));
    expect(hook.result.current.seedReviewOverview.review?.items[0].recommendation).toBe('park');

    act(() => {
      ws.emit({
        event: 'garden_review_updated',
        review: review([reviewItem({ recommendation: 'harvest', explanation: 'The work is done.' })]),
      });
    });
    expect(hook.result.current.seedReviewOverview.review?.items[0].recommendation).toBe('harvest');
  });

  it('moves the overview to a newer review run and rejects an older broadcast', async () => {
    const { ws, hook } = await renderWithGarden();
    let pending!: Promise<unknown>;
    act(() => {
      pending = hook.result.current.sendSeedReviewShow();
    });
    const command = ws.sent.map((raw) => JSON.parse(raw)).find((message) => message.cmd === 'seed_review_show');

    act(() => {
      ws.emit({
        event: 'seed_review_result', request_id: command.request_id, operation: 'show',
        success: true, candidate_count: 0,
        review: review([], { id: 'r-old', status: 'completed', captured_at: '2026-08-30T10:00:00Z' }),
      });
    });
    await expect(pending).resolves.toEqual(expect.objectContaining({ candidateCount: 0 }));

    act(() => {
      ws.emit({
        event: 'garden_review_updated',
        review: review(
          [reviewItem({ id: 'r-new.s-review1', run_id: 'r-new' })],
          { id: 'r-new', captured_at: '2026-08-30T11:00:00Z' },
        ),
      });
    });
    expect(hook.result.current.seedReviewOverview.review?.run.id).toBe('r-new');
    expect(hook.result.current.seedReviewOverview.candidateCount).toBe(1);

    act(() => {
      ws.emit({
        event: 'garden_review_updated',
        review: review(
          [reviewItem({ recommendation: 'wither' })],
          { id: 'r-old', captured_at: '2026-08-30T10:00:00Z' },
        ),
      });
    });
    expect(hook.result.current.seedReviewOverview.review?.run.id).toBe('r-new');
  });

  it('sends the review receipt with a lifecycle move', async () => {
    const { ws, hook } = await renderWithGarden();
    act(() => {
      void hook.result.current.sendSeedTransition(
        's-review1', 'park', undefined, undefined, 'Waiting for input.',
        { reviewId: 'r-1', evidenceVersion: 'evidence-1' },
      );
    });
    const command = ws.sent.map((raw) => JSON.parse(raw)).find((message) => message.cmd === 'seed_transition');
    expect(command).toMatchObject({
      seed_id: 's-review1',
      verb: 'park',
      comment: 'Waiting for input.',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });
  });

  it('keeps a reviewed seed growing with its evidence receipt', async () => {
    const { ws, hook } = await renderWithGarden();
    let pending!: Promise<unknown>;
    act(() => {
      pending = hook.result.current.sendSeedReviewKeep(
        's-review1', { reviewId: 'r-1', evidenceVersion: 'evidence-1' },
      );
    });
    const command = ws.sent.map((raw) => JSON.parse(raw)).find((message) => message.cmd === 'seed_review_keep');
    expect(command).toMatchObject({
      seed_id: 's-review1',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });

    act(() => {
      ws.emit({
        event: 'seed_review_result', request_id: command.request_id, operation: 'keep',
        success: true, candidate_count: 0, review: review(),
      });
    });
    await expect(pending).resolves.toEqual(expect.objectContaining({ candidateCount: 0 }));
  });

  it('returns an advisory handoff draft', async () => {
    const { ws, hook } = await renderWithGarden();
    let pending!: Promise<string>;
    act(() => {
      pending = hook.result.current.sendSeedReviewDraft(
        's-review1', { reviewId: 'r-1', evidenceVersion: 'evidence-1' },
      );
    });
    const command = ws.sent.map((raw) => JSON.parse(raw)).find((message) => message.cmd === 'seed_review_draft');
    expect(command).toMatchObject({
      seed_id: 's-review1',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });

    act(() => {
      ws.emit({
        event: 'seed_review_draft_result', request_id: command.request_id,
        success: true, handoff: 'Inspect the remaining edge and run the focused test.',
      });
    });
    await expect(pending).resolves.toBe('Inspect the remaining edge and run the focused test.');
  });

  it('sends a guarded seed to Chief with optional placement guidance', async () => {
    const { ws, hook } = await renderWithGarden();
    let pending!: Promise<unknown>;
    act(() => {
      pending = hook.result.current.sendSeedToChief({
        seedId: 's-review1',
        expectedRev: 4,
        expectedTenderSession: 'sess-old',
        expectedTenderMember: '',
        sourceSessionId: 'sess-user',
        guidance: 'Use branch feature/special under /tmp/special.',
        review: { reviewId: 'r-1', evidenceVersion: 'evidence-1' },
      });
    });
    const command = ws.sent.map((raw) => JSON.parse(raw))
      .find((message) => message.cmd === 'seed_send_to_chief');
    expect(command).toMatchObject({
      source_session_id: 'sess-user',
      seed_id: 's-review1',
      expected_rev: 4,
      expected_tender_session: 'sess-old',
      expected_tender_member: '',
      guidance: 'Use branch feature/special under /tmp/special.',
      review: { review_id: 'r-1', evidence_version: 'evidence-1' },
    });

    const result = {
      seed: seed('s-review1', 'Review this seed'),
      chief_session_id: 'chief',
      delivery_status: 'queued',
      detail: 'queued for Chief',
    };
    act(() => {
      ws.emit({
        event: 'seed_send_to_chief_result',
        request_id: command.request_id,
        success: true,
        result,
      });
    });
    await expect(pending).resolves.toEqual(result);
  });
});
