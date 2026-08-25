// Duck-typed against pi's ExtensionAPI/ExtensionContext (verified against pi v0.80.10) so
// this file loads under `bun test` with no pi runtime present.
import { createConnection, type Socket } from "node:net";
import {
  relayMethods,
  type RelayDeliverMessageParams,
  type RelayDeliverMessageResult,
  type RelayHelloState,
} from "../src/relay-protocol";

export type SessionStartReason = "startup" | "reload" | "new" | "resume" | "fork";

export type RelayHelloReason = SessionStartReason | "reconnect";

export type SessionStartEvent = { type: "session_start"; reason: SessionStartReason; previousSessionFile?: string };
export type AgentStartEvent = { type: "agent_start" };
export type AgentSettledEvent = { type: "agent_settled" };

export type AgentMessageContentBlock = { type: string; text?: string };
export type AgentMessageLike = { role: string; content: AgentMessageContentBlock[]; stopReason?: string };
export type AgentEndEvent = { type: "agent_end"; messages: AgentMessageLike[] };
export type MessageStartEvent = { type: "message_start"; message: AgentMessageLike };

export type SessionManagerLike = { getSessionId(): string };

export type ExtensionContextLike = {
  isIdle(): boolean;
  readonly sessionManager: SessionManagerLike;
};

export type ExtensionHandler<TEvent> = (event: TEvent, ctx: ExtensionContextLike) => void | Promise<void>;

export type ExtensionAPILike = {
  on(event: "session_start", handler: ExtensionHandler<SessionStartEvent>): void;
  on(event: "agent_start", handler: ExtensionHandler<AgentStartEvent>): void;
  on(event: "message_start", handler: ExtensionHandler<MessageStartEvent>): void;
  on(event: "agent_end", handler: ExtensionHandler<AgentEndEvent>): void;
  on(event: "agent_settled", handler: ExtensionHandler<AgentSettledEvent>): void;
  sendUserMessage(content: string, options?: { deliverAs?: "steer" | "followUp" }): void;
};

type JSONRPCID = number | string;
type JSONRPCRequest = { jsonrpc: "2.0"; id: JSONRPCID; method: string; params?: unknown };
type JSONRPCResponse = { jsonrpc: "2.0"; id: JSONRPCID; result?: unknown; error?: { code: number; message: string } };
type Pending = { resolve: (result: unknown) => void; reject: (error: Error) => void };

type RetainedReport = { method: string; params: unknown; inFlight: boolean };

// Generous: the driver's handler runs an LLM classification before answering.
const suiteRequestTimeoutMs = 60_000;

// Measured on attn's own daemon restarts: the runtime re-registers inside 1s, so the first
// retry is well under that; the ceiling only bounds a driver that is not coming back.
const reconnectMinDelayMs = 500;
const reconnectMaxDelayMs = 30_000;

export class RelaySuiteClient {
  private socket: Socket | undefined;
  private connecting: Promise<Socket> | undefined;
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, Pending>();
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private reconnectDelayMs = reconnectMinDelayMs;
  private closed = false;
  private announced = false;
  private retained: RetainedReport | undefined;
  private readonly retainedFacts = new Map<string, RetainedReport>();
  // Nothing here can log: a stray write corrupts pi's TUI, and the one channel out
  // is the one that just failed, so the count rides the next hello instead.
  private dropped = 0;

  constructor(
    private readonly socketPath: string,
    private readonly onDeliverMessage: (params: RelayDeliverMessageParams) => Promise<RelayDeliverMessageResult>,
    private readonly helloParams: () => unknown | undefined,
  ) {}

  announce(params: unknown): void {
    this.ensureConnected()
      .then((socket) => {
        this.writeHello(socket, params);
        const retained = this.retained;
        if (retained && !retained.inFlight) void this.deliver(retained);
        for (const [key, fact] of this.retainedFacts) {
          if (!fact.inFlight) void this.deliverFact(key, fact);
        }
      })
      .catch(() => {
      });
  }

  report(method: string, params: unknown): void {
    const entry: RetainedReport = { method, params, inFlight: false };
    this.retained = entry;
    void this.deliver(entry);
  }

  private async deliver(entry: RetainedReport): Promise<void> {
    entry.inFlight = true;
    try {
      const socket = await this.ensureConnected();
      this.sayHello(socket);
      await this.request(socket, entry.method, entry.params);
      if (this.retained === entry) this.retained = undefined;
    } catch {
      this.dropped += 1;
    } finally {
      entry.inFlight = false;
    }
  }

  reportFact(key: string, method: string, params: unknown): void {
    const entry: RetainedReport = { method, params, inFlight: false };
    this.retainedFacts.set(key, entry);
    void this.deliverFact(key, entry);
  }

  private async deliverFact(key: string, entry: RetainedReport): Promise<void> {
    entry.inFlight = true;
    let socket: Socket | undefined;
    try {
      socket = await this.ensureConnected();
      this.sayHello(socket);
      await this.request(socket, entry.method, entry.params);
      if (this.retainedFacts.get(key) === entry) this.retainedFacts.delete(key);
    } catch {
      this.dropped += 1;
      if (socket && this.socket === socket) socket.destroy();
    } finally {
      entry.inFlight = false;
    }
  }

  async send(method: string, params: unknown): Promise<void> {
    try {
      const socket = await this.ensureConnected();
      // A report on a connection that has not named its run is answered "unknown
      // token", so the hello goes first.
      this.sayHello(socket);
      await this.request(socket, method, params);
    } catch {
      this.dropped += 1;
    }
  }

  close(): void {
    this.closed = true;
    if (this.reconnectTimer !== undefined) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
    this.socket?.destroy();
    this.socket = undefined;
    this.failPending(new Error("suite relay connection closed"));
  }

  private ensureConnected(): Promise<Socket> {
    if (this.socket && !this.socket.destroyed) return Promise.resolve(this.socket);
    if (!this.connecting) {
      this.connecting = this.dial()
        .catch((error: unknown) => {
          this.scheduleReconnect();
          throw error;
        })
        .finally(() => {
          this.connecting = undefined;
        });
    }
    return this.connecting;
  }

  private dial(): Promise<Socket> {
    return new Promise((resolve, reject) => {
      const socket = createConnection({ path: this.socketPath });
      socket.once("error", reject);
      socket.once("connect", () => {
        socket.off("error", reject);
        socket.setEncoding("utf8");
        socket.on("data", (chunk) => this.consume(chunk));
        socket.on("error", (error) => this.failPending(error));
        socket.on("close", () => {
          if (this.socket === socket) this.socket = undefined;
          this.failPending(new Error("suite relay connection closed"));
          this.scheduleReconnect();
        });
        this.socket = socket;
        this.announced = false;
        this.reconnectDelayMs = reconnectMinDelayMs;
        resolve(socket);
      });
    });
  }

  private scheduleReconnect(): void {
    if (this.closed || this.reconnectTimer !== undefined) return;
    const delay = this.reconnectDelayMs;
    this.reconnectDelayMs = Math.min(delay * 2, reconnectMaxDelayMs);
    const timer = setTimeout(() => {
      this.reconnectTimer = undefined;
      const params = this.helloParams();
      if (params === undefined) return;
      this.announce(params);
    }, delay);
    timer.unref?.();
    this.reconnectTimer = timer;
  }

  private sayHello(socket: Socket): void {
    if (this.announced) return;
    const params = this.helloParams();
    if (params !== undefined) this.writeHello(socket, params);
  }

  private writeHello(socket: Socket, params: unknown): void {
    this.announced = true;
    const dropped = this.dropped;
    this.dropped = 0;
    const withCount =
      dropped > 0 && params !== null && typeof params === "object"
        ? { ...(params as Record<string, unknown>), dropped_reports: dropped }
        : params;
    this.request(socket, relayMethods.hello, withCount).catch(() => {});
  }

  private request(socket: Socket, method: string, params: unknown): Promise<unknown> {
    const id = this.nextID++;
    const result = new Promise<unknown>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(String(id));
        reject(new Error(`relay did not respond to ${method} within ${suiteRequestTimeoutMs}ms`));
      }, suiteRequestTimeoutMs);
      this.pending.set(String(id), {
        resolve: (value) => {
          clearTimeout(timer);
          resolve(value);
        },
        reject: (error) => {
          clearTimeout(timer);
          reject(error);
        },
      });
    });
    socket.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    return result;
  }

  private consume(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n");
      if (end < 0) return;
      const line = this.buffer.slice(0, end).trim();
      this.buffer = this.buffer.slice(end + 1);
      if (line === "") continue;
      void this.route(JSON.parse(line) as JSONRPCRequest | JSONRPCResponse);
    }
  }

  private async route(message: JSONRPCRequest | JSONRPCResponse): Promise<void> {
    if ("method" in message) {
      await this.respond(message);
      return;
    }
    const pending = this.pending.get(String(message.id));
    if (!pending) return;
    this.pending.delete(String(message.id));
    if (message.error) {
      pending.reject(new Error(message.error.message));
      return;
    }
    pending.resolve(message.result);
  }

  private async respond(request: JSONRPCRequest): Promise<void> {
    if (request.method !== relayMethods.deliverMessage) {
      this.send_(request.id, { error: { code: -32601, message: `unknown method ${request.method}` } });
      return;
    }
    try {
      const result = await this.onDeliverMessage(request.params as RelayDeliverMessageParams);
      this.send_(request.id, { result });
    } catch (error) {
      this.send_(request.id, { error: { code: -32603, message: error instanceof Error ? error.message : String(error) } });
    }
  }

  private send_(id: JSONRPCID, outcome: { result: unknown } | { error: { code: number; message: string } }): void {
    if (!this.socket) return;
    this.socket.write(`${JSON.stringify({ jsonrpc: "2.0", id, ...outcome })}\n`);
  }

  private failPending(error: Error): void {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }
}

export type SuiteEnv = {
  socketPath: string | undefined;
  token: string | undefined;
  piVersion: string;
};

export type SuiteDenial = {
  tool: string;
  action: string;
  reason: string;
  rule: string;
  at: string;
};

export class AttnPiSuite {
  private readonly piVersion: string;
  private readonly relay: { client: RelaySuiteClient; token: string } | undefined;

  private currentPi: ExtensionAPILike | undefined;
  private currentContext: ExtensionContextLike | undefined;

  private cachedAssistantText = "";
  private cachedAborted = false;
  private approvalOpen = false;
  private readonly pendingInputs: Array<{ inputID: string; text: string }> = [];

  constructor(env: SuiteEnv) {
    this.piVersion = env.piVersion;
    const socketPath = env.socketPath?.trim();
    const token = env.token?.trim();
    this.relay =
      socketPath && token
        ? {
            client: new RelaySuiteClient(socketPath, this.handleDeliverMessage, () => this.helloParams("reconnect")),
            token,
          }
        : undefined;
  }

  private helloParams(reason: RelayHelloReason): Record<string, unknown> | undefined {
    const relay = this.relay;
    const ctx = this.currentContext;
    if (!relay || !ctx) return undefined;
    return {
      token: relay.token,
      pi_session_id: ctx.sessionManager.getSessionId(),
      pi_version: this.piVersion,
      reason,
      pi_state: this.currentState(ctx),
    };
  }

  private currentState(ctx: ExtensionContextLike): RelayHelloState {
    if (this.approvalOpen) return "pending_approval";
    return ctx.isIdle() ? "idle" : "working";
  }

  register(pi: ExtensionAPILike): void {
    const relay = this.relay;
    if (!relay) return;
    this.currentPi = pi;

    pi.on("session_start", (event, ctx) => {
      this.currentContext = ctx;
      const params = this.helloParams(event.reason);
      if (params) relay.client.announce(params);
    });

    pi.on("agent_start", (_event, ctx) => {
      this.currentContext = ctx;
      relay.client.report(relayMethods.reportState, { token: relay.token, state: "working" });
    });

    pi.on("message_start", (event, ctx) => {
      this.currentContext = ctx;
      if (event.message.role !== "user") return;
      const text = messageText(event.message);
      const index = this.pendingInputs.findIndex((candidate) => candidate.text === text);
      if (index < 0) return;
      const [candidate] = this.pendingInputs.splice(index, 1);
      if (!candidate) return;
      relay.client.reportFact(candidate.inputID, relayMethods.reportInputTaken, {
        token: relay.token,
        input_id: candidate.inputID,
      });
    });

    pi.on("agent_end", (event, ctx) => {
      this.currentContext = ctx;
      const last = lastAssistantMessage(event.messages);
      this.cachedAssistantText = last ? assistantText(last) : "";
      this.cachedAborted = last?.stopReason === "aborted";
    });

    pi.on("agent_settled", (_event, ctx) => {
      this.currentContext = ctx;
      const text = this.cachedAssistantText;
      const aborted = this.cachedAborted;
      this.cachedAssistantText = "";
      this.cachedAborted = false;
      this.approvalOpen = false;
      relay.client.report(relayMethods.reportStop, { token: relay.token, assistant_text: text, aborted });
    });
  }

  reportApprovalWindow(open: boolean): void {
    const relay = this.relay;
    if (!relay || this.approvalOpen === open) return;
    this.approvalOpen = open;
    relay.client.report(relayMethods.reportState, {
      token: relay.token,
      state: open ? "pending_approval" : "working",
    });
  }

  reportDenial(denial: SuiteDenial): void {
    const relay = this.relay;
    if (!relay) return;
    void relay.client.send(relayMethods.reportDenial, {
      token: relay.token,
      tool: denial.tool,
      action: denial.action,
      reason: denial.reason,
      rule: denial.rule,
      at: denial.at,
    });
  }

  close(): void {
    this.relay?.client.close();
  }

  private readonly handleDeliverMessage = async (
    params: RelayDeliverMessageParams,
  ): Promise<RelayDeliverMessageResult> => {
    const pi = this.currentPi;
    const ctx = this.currentContext;
    if (!pi || !ctx) return { delivered: false };
    const candidate = { inputID: params.input_id, text: params.text };
    this.pendingInputs.push(candidate);
    try {
      pi.sendUserMessage(params.text, ctx.isIdle() ? undefined : { deliverAs: "steer" });
      return { delivered: true };
    } catch {
      const index = this.pendingInputs.indexOf(candidate);
      if (index >= 0) this.pendingInputs.splice(index, 1);
      // A stale pi/ctx from a superseded session generation throws here on any use:
      // an ordinary "can't deliver right now", not a bug to rethrow across the wire.
      return { delivered: false };
    }
  };
}

function lastAssistantMessage(messages: AgentMessageLike[]): AgentMessageLike | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i];
    if (message?.role === "assistant") return message;
  }
  return undefined;
}

function assistantText(message: AgentMessageLike): string {
  return message.content
    .filter((block) => block.type === "text" && typeof block.text === "string")
    .map((block) => block.text)
    .join("\n");
}

function messageText(message: AgentMessageLike): string {
  return message.content
    .filter((block) => block.type === "text" && typeof block.text === "string")
    .map((block) => block.text)
    .join("\n");
}
