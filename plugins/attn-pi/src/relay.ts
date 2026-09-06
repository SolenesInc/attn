import { existsSync, unlinkSync } from "node:fs";
import { createServer, type Server, type Socket } from "node:net";
import { relayMethods } from "./relay-protocol";

type JSONRPCID = number | string;

type JSONRPCRequest = {
  jsonrpc: "2.0";
  id: JSONRPCID;
  method: string;
  params?: unknown;
};

type JSONRPCResponse = {
  jsonrpc: "2.0";
  id: JSONRPCID;
  result?: unknown;
  error?: { code: number; message: string };
};

type Pending = {
  resolve: (result: unknown) => void;
  reject: (error: Error) => void;
};

export type RelayDelegate = {
  suiteHello(connection: RelayConnection, params: unknown): Promise<{ ok: true }>;
  suiteReportState(params: unknown): Promise<void>;
  suiteReportStop(params: unknown): Promise<void>;
  suiteReportDenial(params: unknown): Promise<void>;
  suiteReportInputTaken(params: unknown): Promise<void>;
  suiteReportPullRequest(params: unknown): Promise<void>;
  suiteReportSessionFile(params: unknown): Promise<void>;
  suiteReportExecPolicyAmendment(params: unknown): Promise<void>;
  suiteReportNetworkAmendment(params: unknown): Promise<void>;
};

/** A thunk lets the delegate be named before it exists: the driver holds the relay,
 * so it can only be built after it. */
export type RelayDelegateSource = RelayDelegate | (() => RelayDelegate);

/** Every suite-to-driver report answers `{ ok: true }`, so the table is the whole
 * dispatch: a method listed here cannot reach the wire without a delegate method. */
export const suiteReports: Record<string, Exclude<keyof RelayDelegate, "suiteHello">> = {
  [relayMethods.reportState]: "suiteReportState",
  [relayMethods.reportStop]: "suiteReportStop",
  [relayMethods.reportDenial]: "suiteReportDenial",
  [relayMethods.reportInputTaken]: "suiteReportInputTaken",
  [relayMethods.reportPullRequest]: "suiteReportPullRequest",
  [relayMethods.reportSessionFile]: "suiteReportSessionFile",
  [relayMethods.reportExecPolicyAmendment]: "suiteReportExecPolicyAmendment",
  [relayMethods.reportNetworkAmendment]: "suiteReportNetworkAmendment",
};

function resolveDelegate(source: RelayDelegateSource): RelayDelegate {
  return typeof source === "function" ? source() : source;
}

// One RelayConnection per suite that dials in. A request here is driver -> suite (its
// own id space); inbound requests are suite -> driver, dispatched to the delegate.
export class RelayConnection {
  private buffer = "";
  private nextID = 1;
  private readonly pending = new Map<string, Pending>();
  private readonly closeHandlers: Array<() => void> = [];

  constructor(
    private readonly socket: Socket,
    private readonly delegateSource: RelayDelegateSource,
    private readonly log?: (line: string) => void,
  ) {
    this.socket.setEncoding("utf8");
    this.socket.on("data", (chunk) => this.consume(chunk));
    this.socket.on("error", (error) => this.failPending(error));
    this.socket.on("close", () => {
      this.failPending(new Error("suite connection closed"));
      for (const handler of this.closeHandlers) handler();
    });
  }

  onClose(handler: () => void): void {
    this.closeHandlers.push(handler);
  }

  /** Without `timeoutMs` the request stands until the suite answers or the socket
   * closes: a request whose answer is a human decision has no deadline of ours. */
  request<TResult = unknown>(method: string, params: unknown, timeoutMs?: number): Promise<TResult> {
    const id = this.nextID++;
    const result = new Promise<TResult>((resolve, reject) => {
      const timer =
        timeoutMs === undefined
          ? undefined
          : setTimeout(() => {
              this.pending.delete(String(id));
              reject(new Error(`suite did not respond to ${method} within ${timeoutMs}ms`));
            }, timeoutMs);
      this.pending.set(String(id), {
        resolve: (value) => {
          if (timer !== undefined) clearTimeout(timer);
          resolve(value as TResult);
        },
        reject: (error) => {
          if (timer !== undefined) clearTimeout(timer);
          reject(error);
        },
      });
    });
    try {
      this.send({ jsonrpc: "2.0", id, method, params });
    } catch (error) {
      this.pending.delete(String(id));
      throw error;
    }
    return result;
  }

  close(): void {
    this.socket.destroy();
  }

  private consume(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n");
      if (end < 0) return;
      const line = this.buffer.slice(0, end).trim();
      this.buffer = this.buffer.slice(end + 1);
      if (line === "") continue;
      this.route(JSON.parse(line) as JSONRPCRequest | JSONRPCResponse);
    }
  }

  private route(message: JSONRPCRequest | JSONRPCResponse): void {
    if ("method" in message) {
      void this.respond(message);
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
    const handler = this.handlerFor(request.method);
    if (!handler) {
      this.sendError(request.id, -32601, `unknown method ${request.method}`);
      return;
    }
    try {
      this.send({ jsonrpc: "2.0", id: request.id, result: await handler(request.params) });
    } catch (error) {
      // The suite has no channel to speak on, so this log is the only account of a
      // report the driver refused to carry.
      const message = error instanceof Error ? error.message : String(error);
      this.log?.(`relay ${request.method} failed: ${message}`);
      this.sendError(request.id, -32603, message);
    }
  }

  private get delegate(): RelayDelegate {
    return resolveDelegate(this.delegateSource);
  }

  private handlerFor(method: string): ((params: unknown) => Promise<unknown>) | undefined {
    if (method === relayMethods.hello) return (params) => this.delegate.suiteHello(this, params);
    const report = suiteReports[method];
    if (report === undefined) return undefined;
    return async (params) => {
      await this.delegate[report](params);
      return { ok: true };
    };
  }

  private send(message: JSONRPCRequest | JSONRPCResponse): void {
    this.socket.write(`${JSON.stringify(message)}\n`);
  }

  private sendError(id: JSONRPCID, code: number, message: string): void {
    this.send({ jsonrpc: "2.0", id, error: { code, message } });
  }

  private failPending(error: Error): void {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }
}

export class RelayServer {
  readonly socketPath: string;
  private readonly delegate: RelayDelegateSource;
  private readonly log: ((line: string) => void) | undefined;
  private server?: Server;
  private readonly connections = new Set<RelayConnection>();

  constructor(options: { socketPath: string; delegate: RelayDelegateSource; log?: (line: string) => void }) {
    this.socketPath = options.socketPath;
    this.delegate = options.delegate;
    this.log = options.log;
  }

  async listen(): Promise<void> {
    if (existsSync(this.socketPath)) unlinkSync(this.socketPath);
    const server = createServer((socket) => {
      const connection = new RelayConnection(socket, this.delegate, this.log);
      this.connections.add(connection);
      connection.onClose(() => this.connections.delete(connection));
    });
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(this.socketPath, () => {
        server.off("error", reject);
        resolve();
      });
    });
    this.server = server;
  }

  deliverMessage<TParams, TResult>(connection: RelayConnection, params: TParams, timeoutMs: number): Promise<TResult> {
    return connection.request<TResult>(relayMethods.deliverMessage, params, timeoutMs);
  }

  /** Asks the session to decide a held network connection. No timeout: the proxy holds
   * the client until the session's reviewer answers, as Codex does. */
  networkDecide<TParams, TResult>(connection: RelayConnection, params: TParams): Promise<TResult> {
    return connection.request<TResult>(relayMethods.networkDecide, params);
  }

  close(): void {
    for (const connection of this.connections) connection.close();
    this.connections.clear();
    this.server?.close();
    this.server = undefined;
    if (existsSync(this.socketPath)) unlinkSync(this.socketPath);
  }
}
