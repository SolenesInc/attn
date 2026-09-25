import { act } from '@testing-library/react';
import { onTestFinished, vi } from 'vitest';
import { PROTOCOL_VERSION } from '../hooks/useDaemonSocket';
import type { CommandMessage, CommandName, EventMessage } from './protocol';

type CorrelatedReply<T> = T extends unknown
  ? { [K in keyof T as K extends 'request_id' ? never : K]: T[K] } & { request_id?: string }
  : never;

export type Reply = CorrelatedReply<EventMessage>;
export type ReplyHandler<C extends CommandName> = (
  command: CommandMessage<C>,
  connection: DaemonConnection,
) => Reply | Reply[] | void;

type InitialState = EventMessage<'initial_state'>;

export function initialState(overrides: Partial<InitialState> = {}): InitialState {
  return {
    event: 'initial_state',
    protocol_version: PROTOCOL_VERSION,
    sessions: [],
    workspaces: [],
    prs: [],
    repos: [],
    authors: [],
    settings: {},
    ...overrides,
  };
}

const WORKSPACE_SESSIONS_CAPABILITY = 'workspace_sessions';

class DaemonConnection {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 3;

  readyState = DaemonConnection.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readonly sent: CommandMessage[] = [];
  private deliveries = 0;

  constructor(private readonly daemon: ScriptedDaemon) {
    daemon.accept(this);
    queueMicrotask(() => {
      if (this.readyState !== DaemonConnection.CONNECTING) return;
      this.readyState = DaemonConnection.OPEN;
      act(() => this.onopen?.(new Event('open')));
    });
  }

  send(data: string) {
    if (this.readyState === DaemonConnection.CONNECTING) {
      throw new DOMException('send on a connecting socket', 'InvalidStateError');
    }
    if (this.readyState !== DaemonConnection.OPEN) return;
    const command = JSON.parse(data) as CommandMessage;
    this.sent.push(command);
    this.daemon.receive(command, this);
  }

  close(code = 1000) {
    if (this.readyState === DaemonConnection.CLOSED) return;
    this.readyState = DaemonConnection.CLOSED;
    queueMicrotask(() => {
      act(() => this.onclose?.(new CloseEvent('close', { code })));
    });
  }

  get traffic(): number {
    return this.sent.length + this.deliveries;
  }

  emit(message: Reply) {
    if (this.readyState !== DaemonConnection.OPEN) {
      throw new Error('the daemon has no open connection to deliver to');
    }
    this.deliveries++;
    act(() => this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(message) })));
  }
}

export interface ScriptedDaemonOptions {
  initialState?: Partial<InitialState> | false;
}

type AnyHandler = (command: CommandMessage, connection: DaemonConnection) => Reply | Reply[] | void;

interface Waiter {
  matches: (command: CommandMessage) => boolean;
  resolve: (command: CommandMessage) => void;
}

function handshakeRefusal(command: CommandMessage): string | null {
  if (command.cmd !== 'client_hello') return `${command.cmd} arrived before client_hello`;
  if (!command.capabilities?.includes(WORKSPACE_SESSIONS_CAPABILITY)) {
    return `client_hello lacks the ${WORKSPACE_SESSIONS_CAPABILITY} capability`;
  }
  return null;
}

export class ScriptedDaemon {
  readonly connections: DaemonConnection[] = [];
  private readonly handlers = new Map<string, AnyHandler>();
  private readonly violations: string[] = [];
  private readonly commandWaiters = new Set<Waiter>();
  private readonly connectionWaiters: Array<(connection: DaemonConnection) => void> = [];
  private readonly greeted = new WeakSet<DaemonConnection>();
  private readonly refused = new WeakSet<DaemonConnection>();
  private readonly settled = new WeakSet<DaemonConnection>();
  private heldReplies: Array<() => void> | null = null;

  constructor(options: ScriptedDaemonOptions = {}) {
    const handshake = options.initialState;
    this.on('client_hello', () => (handshake === false ? undefined : initialState(handshake)));
  }

  get connection(): DaemonConnection {
    const latest = this.connections[this.connections.length - 1];
    if (!latest) throw new Error('the app has not connected to the daemon');
    return latest;
  }

  get sent(): CommandMessage[] {
    return this.connections.flatMap((connection) => connection.sent);
  }

  sentOf<C extends CommandName>(cmd: C): CommandMessage<C>[] {
    return this.sent.filter((command) => command.cmd === cmd) as CommandMessage<C>[];
  }

  on<C extends CommandName>(cmd: C, reply: ReplyHandler<C>): this {
    this.handlers.set(cmd, (command, connection) => reply(command as CommandMessage<C>, connection));
    return this;
  }

  emit(message: Reply) {
    this.connection.emit(message);
  }

  replyTo(command: CommandMessage, reply: Reply) {
    const origin = this.connections.find((connection) => connection.sent.includes(command));
    if (!origin) throw new Error(`the app never sent this ${command.cmd}`);
    origin.emit(reply);
  }

  disconnect(code = 1006) {
    this.connection.close(code);
  }

  async reconnect(): Promise<DaemonConnection> {
    const count = this.connections.length;
    this.disconnect();
    while (this.connections.length === count) {
      await act(() => vi.advanceTimersToNextTimerAsync());
    }
    return act(() => this.connected());
  }

  connected(): Promise<DaemonConnection> {
    const latest = this.connections[this.connections.length - 1];
    if (latest && latest.readyState === DaemonConnection.OPEN && this.settled.has(latest)) {
      return Promise.resolve(latest);
    }
    return new Promise((resolve) => this.connectionWaiters.push(resolve));
  }

  async idle(): Promise<void> {
    const held: Array<() => void> = [];
    this.heldReplies = held;
    try {
      let before: number;
      do {
        before = this.traffic();
        const due = held.splice(0);
        await act(async () => {
          for (const deliver of due) deliver();
          await vi.advanceTimersByTimeAsync(0);
        });
      } while (this.traffic() !== before);
    } finally {
      this.heldReplies = null;
    }
  }

  private traffic(): number {
    return this.connections.reduce((total, connection) => total + connection.traffic, 0);
  }

  received<C extends CommandName>(
    cmd: C,
    matches: (command: CommandMessage<C>) => boolean = () => true,
  ): Promise<CommandMessage<C>> {
    const accepts = (command: CommandMessage) => command.cmd === cmd && matches(command as CommandMessage<C>);
    return new Promise((resolve: (command: CommandMessage) => void) => {
      const already = this.sent.find(accepts);
      if (already) resolve(already);
      else this.commandWaiters.add({ matches: accepts, resolve });
    }) as Promise<CommandMessage<C>>;
  }

  accept(connection: DaemonConnection) {
    this.connections.push(connection);
  }

  receive(command: CommandMessage, connection: DaemonConnection) {
    for (const waiter of this.commandWaiters) {
      if (waiter.matches(command)) {
        this.commandWaiters.delete(waiter);
        waiter.resolve(command);
      }
    }
    if (this.refused.has(connection)) return;
    if (!this.greeted.has(connection)) {
      const refusal = handshakeRefusal(command);
      if (refusal) {
        this.refuse(connection, command, refusal);
        return;
      }
      this.greeted.add(connection);
    }
    const handler = this.handlers.get(command.cmd);
    if (!handler) return;
    const replies = handler(command, connection);
    const correlated = (Array.isArray(replies) ? replies : replies ? [replies] : []).map((reply) =>
      reply.event === 'command_error' || !('request_id' in command) || command.request_id === undefined
        ? reply
        : { request_id: command.request_id, ...reply },
    );
    const deliver = () => {
      if (connection.readyState === DaemonConnection.OPEN) {
        for (const reply of correlated) connection.emit(reply as Reply);
      }
      if (command.cmd === 'client_hello') this.settle(connection);
    };
    if (this.heldReplies) this.heldReplies.push(deliver);
    else queueMicrotask(deliver);
  }

  verify() {
    if (this.violations.length > 0) {
      throw new Error(`the app broke the daemon handshake: ${this.violations.join('; ')}`);
    }
  }

  private refuse(connection: DaemonConnection, command: CommandMessage, reason: string) {
    this.violations.push(reason);
    this.refused.add(connection);
    queueMicrotask(() => {
      if (connection.readyState !== DaemonConnection.OPEN) return;
      connection.emit({ event: 'command_error', cmd: command.cmd, success: false, error: reason });
      connection.close(1008);
    });
  }

  private settle(connection: DaemonConnection) {
    this.settled.add(connection);
    for (const resolve of this.connectionWaiters.splice(0)) resolve(connection);
  }
}

export function installScriptedDaemon(options: ScriptedDaemonOptions = {}): ScriptedDaemon {
  const daemon = new ScriptedDaemon(options);
  vi.useFakeTimers();
  vi.stubEnv('VITE_FORCE_REAL_PTY', '1');
  vi.stubGlobal('fetch', () => Promise.reject(new TypeError('app wire tests reach no network')));
  vi.stubGlobal(
    'WebSocket',
    class extends DaemonConnection {
      constructor() {
        super(daemon);
      }
    },
  );
  onTestFinished(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    vi.useRealTimers();
    daemon.verify();
  });
  return daemon;
}
