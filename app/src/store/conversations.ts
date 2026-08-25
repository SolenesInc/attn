import { create } from 'zustand';

export interface ConversationMessage {
  id: string;
  role: string;
  text: string;
  streaming: boolean;
}

// Fetched with `agent_tool_detail` rather than held: receipt, a p99 11.6 MB transcript is ~0.4% message text.
export interface ConversationToolDetail {
  text: string;
  patch?: string;
  full: boolean;
  truncated: boolean;
  fullOutputPath?: string;
  error?: string;
}

export interface ConversationToolCall {
  callId: string;
  name: string;
  summary: string;
  files: string[];
  status: 'running' | 'ok' | 'error';
  error?: string;
  hasDetail: boolean;
  hasPatch: boolean;
  truncated: boolean;
  fullOutput: boolean;
  detail?: ConversationToolDetail;
}

export type ConversationItem =
  | ({ kind: 'message' } & ConversationMessage)
  | ({ kind: 'tool' } & ConversationToolCall)
  | ({ kind: 'notice' } & ConversationNotice);

// The key an item is addressed by. It is the host's `snapshotItemKey` and the two have to
// agree: a page is asked for by the key of the oldest item a client holds.
export function conversationItemKey(item: ConversationItem): string {
  return item.kind === 'tool' ? `tool:${item.callId}` : `${item.kind}:${item.id}`;
}

export interface ConversationNotice {
  id: string;
  level: 'info' | 'warn' | 'error';
  text: string;
  done: boolean;
}

export type AgentPromptMode = 'prompt' | 'steer' | 'follow_up';

// What the agent has been sent and not yet read, straight from pi's own queues. The app never
// adds to this itself — a local entry would mean two sources for one truth.
export interface ConversationQueue {
  steering: string[];
  followUp: string[];
}

export interface ConversationState {
  items: ConversationItem[];
  epoch: string;
  // False both when the start has been reached and when nothing has ever said otherwise.
  hasMoreBefore: boolean;
  loadingHistory: boolean;
  droppedBefore: number;
  model: string;
  models: string[];
  running: boolean;
  awaitingRun: boolean;
  ready: boolean;
  queue: ConversationQueue;
  lastSeq: number;
}

const emptyQueue: ConversationQueue = { steering: [], followUp: [] };

const emptyConversation: ConversationState = {
  items: [],
  epoch: '',
  hasMoreBefore: false,
  loadingHistory: false,
  droppedBefore: 0,
  model: '',
  models: [],
  running: false,
  awaitingRun: false,
  ready: false,
  queue: emptyQueue,
  lastSeq: 0,
};

interface ConversationsStore {
  conversations: Record<string, ConversationState>;
  applyEnvelope: (sessionId: string, seq: number, kind: string, body: Record<string, unknown>) => void;
  historyRequested: (sessionId: string) => void;
  promptSent: (sessionId: string) => void;
  hostExited: (sessionId: string) => void;
  clearConversation: (sessionId: string) => void;
  // A transcript outlives its host on purpose, so the sessions list bounds this store.
  retainConversations: (liveSessionIds: string[]) => void;
}

function text(body: Record<string, unknown>, key: string): string {
  const value = body[key];
  return typeof value === 'string' ? value : '';
}

function flag(body: Record<string, unknown>, key: string): boolean {
  return body[key] === true;
}

function count(body: Record<string, unknown>, key: string): number {
  const value = body[key];
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

function stringList(body: Record<string, unknown>, key: string): string[] {
  const value = body[key];
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
}

function replaceItem(
  items: ConversationItem[],
  match: (item: ConversationItem) => boolean,
  update: (item: ConversationItem) => ConversationItem,
): ConversationItem[] | null {
  const index = items.findIndex(match);
  if (index < 0) return null;
  const next = items.slice();
  next[index] = update(next[index]);
  return next;
}

const isTool = (callId: string) => (item: ConversationItem) => item.kind === 'tool' && item.callId === callId;
const isMessage = (id: string) => (item: ConversationItem) => item.kind === 'message' && item.id === id;
const isNotice = (id: string) => (item: ConversationItem) => item.kind === 'notice' && item.id === id;

function queueFrom(value: unknown): ConversationQueue {
  if (!value || typeof value !== 'object') return emptyQueue;
  const body = value as Record<string, unknown>;
  return { steering: stringList(body, 'steering'), followUp: stringList(body, 'followUp') };
}

function noticeLevel(value: string): ConversationNotice['level'] {
  return value === 'warn' || value === 'error' ? value : 'info';
}

function snapshotItems(value: unknown): ConversationItem[] | null {
  if (!Array.isArray(value)) return null;
  return value.map(snapshotItem).filter((item): item is ConversationItem => item !== null);
}

// Reads one snapshot item off the wire, or null. An unrecognised shape is dropped rather
// than rendered as a blank row: the host is pinned to a pi version the app is not.
function snapshotItem(value: unknown): ConversationItem | null {
  if (!value || typeof value !== 'object') return null;
  const body = value as Record<string, unknown>;
  if (body.kind === 'message') {
    const id = text(body, 'id');
    if (!id) return null;
    return {
      kind: 'message',
      id,
      role: text(body, 'role') || 'assistant',
      text: text(body, 'text'),
      streaming: flag(body, 'streaming'),
    };
  }
  if (body.kind === 'notice') {
    const id = text(body, 'id');
    if (!id) return null;
    return { kind: 'notice', id, level: noticeLevel(text(body, 'level')), text: text(body, 'text'), done: flag(body, 'done') };
  }
  if (body.kind === 'tool') {
    const callId = text(body, 'call_id');
    if (!callId) return null;
    const status = text(body, 'status');
    const failure = text(body, 'error');
    return {
      kind: 'tool',
      callId,
      name: text(body, 'name'),
      summary: text(body, 'summary'),
      files: stringList(body, 'files'),
      status: status === 'running' ? 'running' : status === 'error' ? 'error' : 'ok',
      error: failure === '' ? undefined : failure,
      hasDetail: flag(body, 'detail'),
      hasPatch: flag(body, 'patch'),
      truncated: flag(body, 'truncated'),
      fullOutput: flag(body, 'full_output'),
    };
  }
  return null;
}

// Splice when this client holds the window's oldest item, otherwise replace. `droppedBefore`
// is the snapshot's own count: across epochs a rebuilt host re-read pi's session file.
function mergeSnapshotWindow(
  current: ConversationState,
  epoch: string,
  window: ConversationItem[],
  hasMore: boolean,
  dropped: number,
): Pick<ConversationState, 'items' | 'epoch' | 'hasMoreBefore' | 'droppedBefore'> {
  const replace = { items: window, epoch, hasMoreBefore: hasMore, droppedBefore: dropped };
  if (epoch === '' || epoch !== current.epoch || window.length === 0) return replace;
  const anchor = conversationItemKey(window[0]);
  const index = current.items.findIndex((item) => conversationItemKey(item) === anchor);
  if (index <= 0) return replace;
  return {
    items: [...current.items.slice(0, index), ...window],
    epoch,
    hasMoreBefore: current.hasMoreBefore,
    droppedBefore: dropped,
  };
}

function applyToConversation(
  current: ConversationState,
  seq: number,
  kind: string,
  body: Record<string, unknown>,
): ConversationState {
  switch (kind) {
    case 'session_ready': {
      const models = stringList(body, 'models');
      return {
        ...current,
        ready: true,
        running: false,
        awaitingRun: false,
        loadingHistory: false,
        queue: emptyQueue,
        model: text(body, 'model') || current.model,
        models: models.length > 0 ? models : current.models,
      };
    }
    case 'conversation_snapshot': {
      const window = snapshotItems(body.items);
      if (!window) return current;
      const merged = mergeSnapshotWindow(
        current,
        text(body, 'epoch'),
        window,
        flag(body, 'has_more'),
        count(body, 'dropped'),
      );
      return {
        ...current,
        ...merged,
        ready: true,
        running: flag(body, 'running'),
        awaitingRun: false,
        loadingHistory: false,
        queue: queueFrom(body.queue),
      };
    }
    case 'conversation_page': {
      const items = snapshotItems(body.items);
      if (!items || current.items.length === 0) return current;
      if (text(body, 'epoch') !== current.epoch) return current;
      if (text(body, 'before') !== conversationItemKey(current.items[0])) return current;
      const held = new Set(current.items.map(conversationItemKey));
      const older = items.filter((item) => !held.has(conversationItemKey(item)));
      return {
        ...current,
        items: older.length > 0 ? [...older, ...current.items] : current.items,
        hasMoreBefore: flag(body, 'has_more'),
        loadingHistory: false,
      };
    }
    case 'model_changed': {
      const model = text(body, 'model');
      return model === '' || model === current.model ? current : { ...current, model };
    }
    case 'notice': {
      const id = text(body, 'id');
      if (!id) return current;
      const notice: ConversationItem = {
        kind: 'notice',
        id,
        level: noticeLevel(text(body, 'level')),
        text: text(body, 'text'),
        done: flag(body, 'done'),
      };
      const replaced = replaceItem(current.items, isNotice(id), () => notice);
      return { ...current, items: replaced ?? [...current.items, notice] };
    }
    case 'run_started':
      return { ...current, running: true, awaitingRun: false };
    case 'run_settled': {
      // message_end precedes run_settled, so anything still open ended under the run and will
      // never grow again — a tool card still running included, since pi reports every tool it starts.
      const items = current.items.map((item) => {
        if (item.kind === 'message') return item.streaming ? { ...item, streaming: false } : item;
        if (item.kind !== 'tool' || item.status !== 'running') return item;
        return { ...item, status: 'error' as const, error: 'the run ended before this tool reported' };
      });
      const failure = text(body, 'error');
      if (failure !== '') {
        items.push({ kind: 'message', id: `error-${seq}`, role: 'error', text: failure, streaming: false });
      }
      return { ...current, running: false, awaitingRun: false, items, queue: emptyQueue };
    }
    case 'queue_update':
      return { ...current, queue: { steering: stringList(body, 'steering'), followUp: stringList(body, 'followUp') } };
    case 'tool_started': {
      const callId = text(body, 'call_id');
      if (!callId || current.items.some(isTool(callId))) return current;
      return {
        ...current,
        items: [...current.items, {
          kind: 'tool',
          callId,
          name: text(body, 'name'),
          summary: text(body, 'summary'),
          files: stringList(body, 'files'),
          status: 'running',
          hasDetail: false,
          hasPatch: false,
          truncated: false,
          fullOutput: false,
        }],
      };
    }
    case 'tool_finished': {
      const callId = text(body, 'call_id');
      if (!callId) return current;
      const failure = text(body, 'error');
      const finished = (item: ConversationToolCall): ConversationItem => ({
        ...item,
        kind: 'tool',
        name: text(body, 'name') || item.name,
        summary: text(body, 'summary') || item.summary,
        files: stringList(body, 'files').length > 0 ? stringList(body, 'files') : item.files,
        status: text(body, 'status') === 'error' ? 'error' : 'ok',
        error: failure === '' ? undefined : failure,
        hasDetail: flag(body, 'detail'),
        hasPatch: flag(body, 'patch'),
        truncated: flag(body, 'truncated'),
        fullOutput: flag(body, 'full_output'),
      });
      const replaced = replaceItem(current.items, isTool(callId), (item) => finished(item as ConversationToolCall));
      if (replaced) return { ...current, items: replaced };
      return {
        ...current,
        items: [...current.items, finished({
          callId,
          name: '',
          summary: '',
          files: [],
          status: 'ok',
          hasDetail: false,
          hasPatch: false,
          truncated: false,
          fullOutput: false,
        })],
      };
    }
    case 'tool_detail': {
      const callId = text(body, 'call_id');
      if (!callId) return current;
      const patch = text(body, 'patch');
      const failure = text(body, 'error');
      const fullOutputPath = text(body, 'full_output_path');
      const detail: ConversationToolDetail = {
        text: text(body, 'text'),
        full: flag(body, 'full'),
        truncated: flag(body, 'truncated'),
        ...(patch === '' ? {} : { patch }),
        ...(fullOutputPath === '' ? {} : { fullOutputPath }),
        ...(failure === '' ? {} : { error: failure }),
      };
      const replaced = replaceItem(current.items, isTool(callId), (item) => {
        const tool = item as ConversationToolCall;
        if (tool.detail?.full && !detail.full) return item;
        return { ...tool, kind: 'tool', detail };
      });
      return replaced ? { ...current, items: replaced } : current;
    }
    case 'message_start': {
      const id = text(body, 'id');
      if (!id || current.items.some(isMessage(id))) return current;
      return {
        ...current,
        items: [...current.items, {
          kind: 'message',
          id,
          role: text(body, 'role') || 'assistant',
          text: '',
          streaming: true,
        }],
      };
    }
    case 'message_delta': {
      const id = text(body, 'id');
      const delta = text(body, 'text');
      if (!id || delta === '') return current;
      const replaced = replaceItem(current.items, isMessage(id), (item) => ({
        ...(item as ConversationMessage),
        kind: 'message',
        text: (item as ConversationMessage).text + delta,
      }));
      if (replaced) return { ...current, items: replaced };
      return {
        ...current,
        items: [...current.items, { kind: 'message', id, role: 'assistant', text: delta, streaming: true }],
      };
    }
    case 'message_end': {
      const id = text(body, 'id');
      if (!id) return current;
      const settled = {
        kind: 'message' as const,
        id,
        role: text(body, 'role') || 'assistant',
        text: text(body, 'text'),
        streaming: false,
      };
      const replaced = replaceItem(current.items, isMessage(id), () => settled);
      return { ...current, items: replaced ?? [...current.items, settled] };
    }
    default:
      return current;
  }
}

export const useConversationsStore = create<ConversationsStore>((set) => ({
  conversations: {},

  applyEnvelope: (sessionId, seq, kind, body) => set((state) => {
    const current = state.conversations[sessionId] ?? emptyConversation;
    // A revived session is a NEW host minting seq from 1 again, so every envelope would read
    // as a duplicate. `session_ready` is exempt from the guard and resets the cursor.
    const spineReset = kind === 'session_ready';
    if (!spineReset && seq > 0 && seq <= current.lastSeq) return state;
    const base = spineReset ? { ...current, lastSeq: 0 } : current;
    const lastSeq = spineReset ? seq : Math.max(current.lastSeq, seq);
    const next = applyToConversation(base, seq, kind, body);
    if (next === base && lastSeq === current.lastSeq) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...next, lastSeq } },
    };
  }),

  historyRequested: (sessionId) => set((state) => {
    const current = state.conversations[sessionId];
    if (!current || current.loadingHistory) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...current, loadingHistory: true } },
    };
  }),

  // The run opens at send time, not on run_started: the round trip is long enough to press
  // Enter twice in and the host drops a mid-run prompt silently.
  promptSent: (sessionId) => set((state) => {
    const current = state.conversations[sessionId] ?? emptyConversation;
    if (!current.ready || current.running) return state;
    return {
      conversations: { ...state.conversations, [sessionId]: { ...current, running: true, awaitingRun: true } },
    };
  }),

  hostExited: (sessionId) => set((state) => {
    const current = state.conversations[sessionId];
    if (!current || (!current.ready && !current.running && !current.awaitingRun && !current.loadingHistory)) return state;
    return {
      conversations: {
        ...state.conversations,
        [sessionId]: { ...current, ready: false, running: false, awaitingRun: false, loadingHistory: false, queue: emptyQueue },
      },
    };
  }),

  clearConversation: (sessionId) => set((state) => {
    if (!(sessionId in state.conversations)) return state;
    const conversations = { ...state.conversations };
    delete conversations[sessionId];
    return { conversations };
  }),

  retainConversations: (liveSessionIds) => set((state) => {
    const live = new Set(liveSessionIds);
    const stale = Object.keys(state.conversations).filter((id) => !live.has(id));
    if (stale.length === 0) return state;
    const conversations = { ...state.conversations };
    for (const id of stale) delete conversations[id];
    return { conversations };
  }),
}));

export function selectConversation(sessionId: string) {
  return (state: ConversationsStore): ConversationState => state.conversations[sessionId] ?? emptyConversation;
}
