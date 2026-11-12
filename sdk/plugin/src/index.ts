import { createConnection, type Socket } from "node:net";

import {
  driverCapabilities, serverRegistration,
  explainRegistrationError,
  type DriverCompactParams,
  type DriverCompactionOutcome,
  type DriverDefinition,
  type DriverDeliverParams,
  type DriverHealth,
  type DriverReportedState,
  type DriverRun,
  type DriverSessionClosedParams,
  type DriverSpawnParams,
  type DriverStopVerdict,
} from "./driver";

export { accepted, deferred, rejected, targetEnded } from "./driver";
export type * from "./driver";

export type ProviderDecline = {
  status: "decline";
};

export type ProviderError = {
  status: "error";
  error: string;
};

export type ProviderHandled<T extends object = Record<string, never>> = {
  status: "handled";
} & T;

export type ProviderResult<T extends object = Record<string, never>> =
  | ProviderHandled<T>
  | ProviderDecline
  | ProviderError;

export type WorktreeCreateParams = {
  main_repo: string;
  branch: string;
  starting_from?: string;
  requested_path?: string | null;
};

export type WorktreeCreateHandled = {
  path: string;
  branch: string;
};

export type WorktreeCreateResult = ProviderResult<WorktreeCreateHandled>;

export type WorktreeDeleteParams = {
  main_repo: string;
  path: string;
  branch?: string;
  force: boolean;
};

export type WorktreeDeleteResult = ProviderResult;

export type WorktreeBeforeCreateParams = WorktreeCreateParams;

export type WorktreeAfterCreateParams = {
  main_repo: string;
  path: string;
  branch: string;
};

export type PluginSurface =
  | "worktree.before_create"
  | "worktree.create"
  | "worktree.after_create"
  | "worktree.delete";

type PluginSurfaceParams = {
  "worktree.before_create": WorktreeBeforeCreateParams;
  "worktree.create": WorktreeCreateParams;
  "worktree.after_create": WorktreeAfterCreateParams;
  "worktree.delete": WorktreeDeleteParams;
};

type PluginSurfaceResult = {
  "worktree.before_create": void;
  "worktree.create": WorktreeCreateResult;
  "worktree.after_create": void;
  "worktree.delete": WorktreeDeleteResult;
};

export type AttnPluginClientOptions = {
  socketPath?: string;
  name?: string;
  version: string;
};

type ResolvedAttnPluginClientOptions = {
  socketPath: string;
  name: string;
  version: string;
  generation: number;
};

export type PluginHandler<TParams = unknown, TResult = unknown> = (
  params: TParams,
) => Promise<TResult> | TResult;

type JsonRpcID = string | number;

type JsonRpcRequest = {
  jsonrpc: "2.0";
  id: JsonRpcID;
  method: string;
  params?: unknown;
};

type JsonRpcResponse = {
  jsonrpc: "2.0";
  id: JsonRpcID;
  result?: unknown;
  error?: {
    code: number;
    message: string;
  };
};

type JsonRpcMessage = JsonRpcRequest | JsonRpcResponse;

type PendingRequest = {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
};

type HelloResult = {
  ok: boolean;
  active_runs?: ActiveRun[];
};

type ActiveRun = { session_id: string; run_id: string; metadata?: unknown; seq: number };

type RegisterResult = {
  ok: boolean;
  active_runs?: ActiveRun[];
};

const pluginAPIVersion = 7;

export class AttnPluginClient {
  private socket?: Socket;
  private nextID = 1;
  private readBuffer = "";
  private readonly handlers = new Map<PluginSurface, PluginHandler>();
  private readonly pending = new Map<string, PendingRequest>();
  private definition?: DriverDefinition;
  private readonly runs = new Map<string, ClientRun>();
  private driverReady = false;
  private readonly closedDuringRecovery: JsonRpcRequest[] = [];

  private readonly options: ResolvedAttnPluginClientOptions;

  constructor(options: AttnPluginClientOptions) {
    this.options = {
      socketPath: resolvePluginOption(options.socketPath, "ATTN_SOCKET_PATH", "socketPath"),
      name: resolvePluginOption(options.name, "ATTN_PLUGIN_NAME", "name"),
      version: options.version,
      generation: resolvePluginGeneration(),
    };
  }

  async connect(): Promise<void> {
    if (this.socket) {
      return;
    }

    this.driverReady = false;
    this.closedDuringRecovery.length = 0;
    this.runs.clear();
    try {
      this.socket = await new Promise<Socket>((resolve, reject) => {
        const socket = createConnection({ path: this.options.socketPath });
        const onError = (error: Error) => reject(error);
        socket.once("error", onError);
        socket.once("connect", () => {
          socket.off("error", onError);
          resolve(socket);
        });
      });

      this.socket.setEncoding("utf8");
      this.socket.on("data", (chunk) => this.consume(chunk));
      this.socket.on("error", (error) => this.failPending(error));
      this.socket.on("close", () => {
        this.socket = undefined;
        this.failPending(new Error("attn plugin socket closed"));
      });

      const result = await this.request<HelloResult>("hello", {
        name: this.options.name,
        version: this.options.version,
        attn_api_version: pluginAPIVersion,
        generation: this.options.generation,
        surfaces: [...this.handlers.keys()].sort(),
      });
      if (!result.ok) {
        throw new Error("attn rejected plugin hello");
      }
      await this.registerDriver(result.active_runs);
    } catch (error) {
      this.resetSocket(error instanceof Error ? error : new Error(String(error)));
      throw error;
    }
  }

  close(): void {
    this.driverReady = false;
    this.socket?.end();
    this.socket = undefined;
    this.failPending(new Error("attn plugin client closed"));
  }

  handle<TSurface extends PluginSurface>(
    surface: TSurface,
    handler: PluginHandler<PluginSurfaceParams[TSurface], PluginSurfaceResult[TSurface]>,
  ): void {
    if (this.socket) {
      throw new Error("attn plugin surfaces must be registered before connect");
    }
    this.handlers.set(surface, handler as PluginHandler);
  }

  driver(definition: DriverDefinition): void {
    if (this.socket) {
      throw new Error("attn plugin driver must be registered before connect");
    }
    if (this.definition) {
      throw new Error(`attn plugin client already registers driver ${this.definition.id}`);
    }
    driverCapabilities(definition);
    this.definition = definition;
  }

  private async registerDriver(activeRuns: ActiveRun[] | undefined): Promise<void> {
    const definition = this.definition;
    if (!definition) {
      return;
    }
    if (activeRuns === undefined) {
      activeRuns = (await this.sendDriverRegistration(definition)).active_runs ?? [];
    }
    const active = activeRuns.map((run) => {
      const driverRun = this.runFor(run.session_id, run.run_id);
      driverRun.restore(run.seq, run.metadata);
      return driverRun;
    });
    await definition.recover?.(active);
    while (this.closedDuringRecovery.length > 0) {
      await this.handleDriverRequest(this.closedDuringRecovery.shift()!);
    }
    this.driverReady = true;
    await this.sendDriverRegistration(definition);
  }

  private async sendDriverRegistration(definition: DriverDefinition): Promise<RegisterResult> {
    try {
      return await this.request<RegisterResult>("driver.register", {
        agent: definition.id,
        capabilities: driverCapabilities(definition),
        server: serverRegistration(definition.server),
      });
    } catch (error) {
      throw explainRegistrationError(error instanceof Error ? error : new Error(String(error)));
    }
  }

  private runFor(terminalId: string, runId: string): ClientRun {
    let run = this.runs.get(runId);
    if (!run) {
      run = new ClientRun(terminalId, runId, (method, params) => this.request(method, params));
      this.runs.set(runId, run);
    }
    return run;
  }

  private async request<TResult = unknown>(method: string, params?: unknown): Promise<TResult> {
    const id = this.nextID++;
    const response = new Promise<TResult>((resolve, reject) => {
      this.pending.set(String(id), {
        resolve: (value) => resolve(value as TResult),
        reject,
      });
    });

    try {
      this.send({
        jsonrpc: "2.0",
        id,
        method,
        params,
      });
    } catch (error) {
      this.pending.delete(String(id));
      throw error;
    }

    return response;
  }

  private consume(chunk: string): void {
    this.readBuffer += chunk;
    for (;;) {
      const newline = this.readBuffer.indexOf("\n");
      if (newline === -1) {
        return;
      }
      const line = this.readBuffer.slice(0, newline).trim();
      this.readBuffer = this.readBuffer.slice(newline + 1);
      if (line === "") {
        continue;
      }
      this.route(JSON.parse(line) as JsonRpcMessage);
    }
  }

  private route(message: JsonRpcMessage): void {
    if ("method" in message) {
      void this.handleRequest(message);
      return;
    }

    const pending = this.pending.get(String(message.id));
    if (!pending) {
      return;
    }
    this.pending.delete(String(message.id));
    if (message.error) {
      pending.reject(new Error(message.error.message));
      return;
    }
    pending.resolve(message.result);
  }

  private async handleRequest(request: JsonRpcRequest): Promise<void> {
    if (request.method === "attn.health") {
      try {
        this.send({
          jsonrpc: "2.0",
          id: request.id,
          result: (await this.definition?.health?.()) ?? ({ ok: true } satisfies DriverHealth),
        });
      } catch (error) {
        this.sendError(request.id, -32603, error instanceof Error ? error.message : String(error));
      }
      return;
    }

    if (request.method.startsWith("driver.")) {
      if (this.definition && !this.driverReady) {
        if (request.method === "driver.session_closed") {
          this.closedDuringRecovery.push(request);
        } else if (request.method === "driver.deliver_message" || request.method === "driver.compact") {
          this.send({ jsonrpc: "2.0", id: request.id, result: { ok: false, status: "deferred", reason: "Driver recovery in progress" } });
        } else {
          this.sendError(request.id, -32603, "Driver recovery in progress");
        }
        return;
      }
      await this.handleDriverRequest(request);
      return;
    }

    const handler = this.handlers.get(request.method as PluginSurface);
    if (!handler) {
      this.sendError(request.id, -32601, `unknown method ${request.method}`);
      return;
    }

    try {
      const result = await handler(request.params);
      this.send({
        jsonrpc: "2.0",
        id: request.id,
        result,
      });
    } catch (error) {
      this.sendError(request.id, -32603, error instanceof Error ? error.message : String(error));
    }
  }

  private async handleDriverRequest(request: JsonRpcRequest): Promise<void> {
    const definition = this.definition;
    try {
      const result = await this.dispatchDriver(definition, request);
      if (result === undefined) {
        this.sendError(request.id, -32601, `unknown method ${request.method}`);
        return;
      }
      this.send({ jsonrpc: "2.0", id: request.id, result });
    } catch (error) {
      this.sendError(request.id, -32603, error instanceof Error ? error.message : String(error));
    }
  }

  private async dispatchDriver(definition: DriverDefinition | undefined, request: JsonRpcRequest): Promise<unknown> {
    if (!definition) {
      return undefined;
    }
    const params = request.params as Record<string, unknown>;
    const run = () => this.runFor(params.session_id as string, params.run_id as string);
    switch (request.method) {
      case "driver.spawn":
        return definition.spawn(params as DriverSpawnParams, run());
      case "driver.resume":
        return definition.resume?.(params as DriverSpawnParams, run());
      case "driver.resume_available": {
        if (!definition.resumeAvailable) return undefined;
        const conversations = (params as { conversations: Parameters<NonNullable<DriverDefinition["resumeAvailable"]>>[0] }).conversations;
        return { availability: await definition.resumeAvailable(conversations) };
      }
      case "driver.models":
        return definition.models?.();
      case "driver.deliver_message":
        return definition.deliver?.(params as DriverDeliverParams, run());
      case "driver.compact":
        return definition.compact?.(params as DriverCompactParams, run());
      case "driver.session_closed": {
        const closed = params as DriverSessionClosedParams;
        await definition.sessionClosed?.(closed);
        this.runs.delete(closed.run_id);
        return { ok: true };
      }
      default:
        return undefined;
    }
  }

  private send(message: JsonRpcMessage): void {
    if (!this.socket) {
      throw new Error("attn plugin socket is not connected");
    }
    this.socket.write(`${JSON.stringify(message)}\n`);
  }

  private sendError(id: JsonRpcID, code: number, message: string): void {
    this.send({
      jsonrpc: "2.0",
      id,
      error: { code, message },
    });
  }

  private failPending(error: Error): void {
    for (const pending of this.pending.values()) {
      pending.reject(error);
    }
    this.pending.clear();
  }

  private resetSocket(error: Error): void {
    const socket = this.socket;
    this.socket = undefined;
    this.failPending(error);
    socket?.destroy();
  }
}

export function handled<T extends object = Record<string, never>>(payload?: T): ProviderHandled<T> {
  return {
    status: "handled",
    ...(payload ?? ({} as T)),
  };
}

export function decline(): ProviderDecline {
  return { status: "decline" };
}

export function providerError(error: string): ProviderError {
  return {
    status: "error",
    error,
  };
}

function resolvePluginOption(
  explicitValue: string | undefined,
  envName: "ATTN_SOCKET_PATH" | "ATTN_PLUGIN_NAME",
  optionName: "socketPath" | "name",
): string {
  const value = explicitValue?.trim() || process.env[envName]?.trim();
  if (!value) {
    throw new Error(`attn plugin ${optionName} is required; pass ${optionName} or set ${envName}`);
  }
  return value;
}

function resolvePluginGeneration(): number {
  const raw = process.env.ATTN_PLUGIN_GENERATION?.trim();
  if (!raw) return 1;
  const generation = Number(raw);
  if (!Number.isSafeInteger(generation) || generation <= 0) {
    throw new Error("ATTN_PLUGIN_GENERATION must be a positive integer");
  }
  return generation;
}

class ClientRun implements DriverRun {
  private seq = 0;
  private reported: unknown;

  constructor(
    readonly terminalId: string,
    readonly runId: string,
    private readonly call: (method: string, params: unknown) => Promise<unknown>,
  ) {}

  get metadata(): unknown {
    return this.reported;
  }

  restore(seq: number, metadata: unknown): void {
    this.seq = Math.max(this.seq, seq);
    this.reported = metadata;
  }

  state(state: DriverReportedState, options: { onlyIfUnknown?: boolean } = {}): Promise<void> {
    return this.report("session.report_state", { state, only_if_unknown: options.onlyIfUnknown || undefined });
  }

  async classifyStop(assistantText: string): Promise<DriverStopVerdict> {
    const result = (await this.call("attn.classify_stop", {
      session_id: this.terminalId,
      run_id: this.runId,
      assistant_text: assistantText,
    })) as { verdict: DriverStopVerdict };
    return result.verdict;
  }

  stop(verdict: DriverStopVerdict): Promise<void> {
    return this.report("session.report_stop", { verdict });
  }

  reportMetadata(metadata: unknown, options: { resumeSessionId?: string } = {}): Promise<void> {
    this.reported = metadata;
    return this.report("session.report_metadata", { metadata, resume_session_id: options.resumeSessionId });
  }

  transcriptPath(path: string): Promise<void> {
    return this.call("session.report_transcript_path", this.identity({ path })).then(() => undefined);
  }

  pullRequest(url: string): Promise<void> {
    return this.call("session.report_pull_request", this.identity({ url })).then(() => undefined);
  }

  compaction(report: { inputId: string; conversationId: string; outcome: DriverCompactionOutcome; reason?: string }): Promise<void> {
    return this.call(
      "session.report_compaction",
      this.identity({
        input_id: report.inputId,
        conversation_id: report.conversationId,
        outcome: report.outcome,
        reason: report.reason,
      }),
    ).then(() => undefined);
  }

  private report(method: string, fields: Record<string, unknown>): Promise<void> {
    this.seq += 1;
    return this.call(method, this.identity({ ...fields, seq: this.seq })).then(() => undefined);
  }

  private identity(fields: Record<string, unknown>): Record<string, unknown> {
    return { session_id: this.terminalId, run_id: this.runId, ...fields };
  }
}
