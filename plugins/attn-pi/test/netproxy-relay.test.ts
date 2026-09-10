import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { createConnection, createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { NetworkRequest } from "../netproxy";
import { PiDriver, type CommandResult, type RunCommand } from "../src/driver";
import { RelayServer, type RelayConnection, type RelayDelegate } from "../src/relay";
import { relayMethods, type RelayNetworkDecideResult } from "../src/relay-protocol";
import type { DriverSpawnParams } from "../src/types";
import { AttnPiSuite, type ExtensionContextLike, type ExtensionHandler, type SessionManagerLike } from "../suite/core";

const cleanups: Array<() => Promise<void> | void> = [];

afterEach(async () => {
  while (cleanups.length > 0) await cleanups.pop()?.();
});

// Keep filenames short: macOS unix socket paths cap sun_path at 104 bytes.
const tmpRoot = mkdtempSync(join(tmpdir(), "attn-np-"));
const suitePath = join(tmpRoot, "suite.js");
writeFileSync(suitePath, "// fake pi suite entrypoint\n");
let socketCounter = 0;

function nextSocketPath(): string {
  return join(tmpRoot, `s${socketCounter++}.sock`);
}

class FakeRPC {
  readonly requests: Array<{ method: string; params: unknown }> = [];

  constructor(private readonly registerResult: unknown = { ok: true, active_runs: [] }) {}

  async request(method: string, params: unknown): Promise<unknown> {
    this.requests.push({ method, params });
    if (method === "driver.register") return this.registerResult;
    return { ok: true };
  }

  handle(): void {}
}

const fakeRunCommand: RunCommand = async () => ({ exitCode: 0, stdout: "0.80.10\n", stderr: "" }) as CommandResult;

async function startUpstream(): Promise<number> {
  const server: Server = createServer((socket) => {
    socket.once("data", () => socket.end("HTTP/1.1 200 OK\r\ncontent-length: 8\r\nconnection: close\r\n\r\nUPSTREAM"));
  });
  await new Promise<void>((resolve) => server.listen({ host: "127.0.0.1", port: 0 }, () => resolve()));
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("upstream has no TCP address");
  cleanups.push(() => new Promise<void>((resolve) => server.close(() => resolve())));
  return address.port;
}

type DriverOptions = { socketPath?: string; stateDir?: string; registerResult?: unknown; initialize?: boolean };

type BuiltDriver = { relay: RelayServer; driver: PiDriver; socketPath: string; stateDir: string };

async function buildDriver(options: DriverOptions = {}): Promise<BuiltDriver> {
  const socketPath = options.socketPath ?? nextSocketPath();
  const stateDir = options.stateDir ?? mkdtempSync(join(tmpdir(), "attn-np-state-"));
  let driver: PiDriver;
  const relay = new RelayServer({
    socketPath,
    delegate: {
      suiteHello: (connection: RelayConnection, params: unknown) => driver.suiteHello(connection, params),
      suiteReportState: (params: unknown) => driver.suiteReportState(params),
      suiteReportProxyCommands: (params: unknown) => driver.suiteReportProxyCommands(params),
      suiteReportStop: (params: unknown) => driver.suiteReportStop(params),
      suiteReportDenial: (params: unknown) => driver.suiteReportDenial(params),
      suiteReportInputTaken: (params: unknown) => driver.suiteReportInputTaken(params),
      suiteReportPullRequest: (params: unknown) => driver.suiteReportPullRequest(params),
    },
  });
  driver = new PiDriver({
    rpc: new FakeRPC(options.registerResult) as never,
    relay,
    suitePath,
    runCommand: fakeRunCommand,
    env: {},
    proxyStateDir: stateDir,
  });
  if (options.initialize) await driver.initialize();
  else await relay.listen();
  cleanups.push(async () => {
    await driver.close();
    relay.close();
  });
  return { driver, relay, socketPath, stateDir };
}

const openNetwork = { enabled: true, allowed_domains: [], denied_domains: [], allow_local_binding: true };

function spawnParams(network: unknown, overrides?: Partial<DriverSpawnParams>): DriverSpawnParams {
  return {
    session_id: "session-1",
    run_id: "run-1",
    cwd: tmpRoot,
    auto_mode: { enabled_default: true, network },
    ...overrides,
  };
}

class FakeSuite {
  private buffer = "";
  readonly asked: Array<{ method: string; params: unknown }> = [];
  answer: unknown = { decision: "allow" };
  readonly greeted: Promise<void>;
  private acknowledge: () => void = () => {};

  private constructor(private readonly socket: Socket) {
    this.greeted = new Promise<void>((resolve) => {
      this.acknowledge = resolve;
    });
    socket.setEncoding("utf8");
    socket.on("data", (chunk: string) => this.consume(chunk));
  }

  static async connect(socketPath: string, token: string, proxyCredentials?: string): Promise<FakeSuite> {
    const socket = await new Promise<Socket>((resolve, reject) => {
      const candidate = createConnection({ path: socketPath });
      candidate.once("error", reject);
      candidate.once("connect", () => {
        candidate.off("error", reject);
        resolve(candidate);
      });
    });
    const suite = new FakeSuite(socket);
    cleanups.push(() => {
      socket.destroy();
    });
    socket.write(
      `${JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: relayMethods.hello,
        params: {
          token,
          pi_session_id: "pi-1",
          pi_version: "0.80.10",
          reason: "startup",
          ...(proxyCredentials ? { proxy_credentials: proxyCredentials } : {}),
        },
      })}\n`,
    );
    return suite;
  }

  private consume(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const end = this.buffer.indexOf("\n");
      if (end < 0) return;
      const line = this.buffer.slice(0, end).trim();
      this.buffer = this.buffer.slice(end + 1);
      if (line === "") continue;
      const message = JSON.parse(line) as { id?: number; method?: string; params?: unknown };
      if (message.method === undefined) {
        this.acknowledge();
        continue;
      }
      this.asked.push({ method: message.method, params: message.params });
      this.socket.write(`${JSON.stringify({ jsonrpc: "2.0", id: message.id, result: this.answer })}\n`);
    }
  }
}

async function proxyConnect(address: string, token: string, host: string, port: number): Promise<string> {
  const [proxyHost = "", proxyPort = ""] = address.split(":");
  const socket = await new Promise<Socket>((resolve, reject) => {
    const candidate = createConnection({ host: proxyHost, port: Number(proxyPort) });
    candidate.once("error", reject);
    candidate.once("connect", () => {
      candidate.off("error", reject);
      resolve(candidate);
    });
  });
  cleanups.push(() => {
      socket.destroy();
    });
  const basic = Buffer.from(`${token}:`, "utf8").toString("base64");
  socket.write(`CONNECT ${host}:${port} HTTP/1.1\r\nHost: ${host}:${port}\r\nProxy-Authorization: Basic ${basic}\r\n\r\n`);
  return new Promise<string>((resolve) => {
    let seen = "";
    socket.on("data", (chunk: Buffer) => {
      seen += chunk.toString("utf8");
      if (seen.includes("\r\n\r\n")) resolve(seen);
    });
    socket.on("close", () => resolve(seen));
  });
}

describe("the driver hosts the proxy and decides through the relay", () => {
  test("a spawn that asks for network policy hands the session the proxy address", async () => {
    const { driver } = await buildDriver();

    const spawned = await driver.spawn(spawnParams(openNetwork));

    expect(spawned.env?.ATTN_PI_PROXY_ADDR).toMatch(/^127\.0\.0\.1:\d+$/);
    expect(spawned.env?.ATTN_PI_PROXY_CREDENTIALS).toMatch(/^[0-9a-f-]{36}$/);
    // The sandboxed command can see the proxy credentials, so they must never equal the relay token.
    expect(spawned.env?.ATTN_PI_PROXY_CREDENTIALS).not.toBe(spawned.env?.ATTN_PI_TOKEN);
    expect(driver.networkProxy()?.evaluateHost(spawned.env?.ATTN_PI_PROXY_CREDENTIALS ?? "", "example.com")).toBe("ask");
  });

  test("a spawn without network policy starts no proxy", async () => {
    const { driver } = await buildDriver();

    const spawned = await driver.spawn(spawnParams({ ...openNetwork, enabled: false }));

    expect(spawned.env?.ATTN_PI_PROXY_ADDR).toBeUndefined();
    expect(driver.networkProxy()).toBeUndefined();
  });

  test("an allowlist miss is decided by the session that owns the connection", async () => {
    const upstream = await startUpstream();
    const { driver, socketPath } = await buildDriver();
    const spawned = await driver.spawn(spawnParams(openNetwork));
    const token = spawned.env?.ATTN_PI_TOKEN ?? "";
    const credentials = spawned.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";
    const suite = await FakeSuite.connect(socketPath, token);

    const response = await proxyConnect(spawned.env?.ATTN_PI_PROXY_ADDR ?? "", credentials, "127.0.0.1", upstream);

    expect(response).toContain("200 Connection established");
    expect(suite.asked).toEqual([
      { method: relayMethods.networkDecide, params: { credentials, host: "127.0.0.1", port: upstream, protocol: "https_connect" } },
    ]);
  });

  test("a session with no live suite cannot decide, so the connection is denied", async () => {
    const upstream = await startUpstream();
    const { driver } = await buildDriver();
    const spawned = await driver.spawn(spawnParams(openNetwork));
    const credentials = spawned.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";

    const response = await proxyConnect(spawned.env?.ATTN_PI_PROXY_ADDR ?? "", credentials, "127.0.0.1", upstream);

    expect(response).toContain("403 Forbidden");
    expect(driver.networkProxy()?.denials(credentials)[0]?.reason).toBe("decider_unavailable");
  });

  test("automode.policy_changed reaches the running proxy without a relaunch", async () => {
    const upstream = await startUpstream();
    const { driver, socketPath } = await buildDriver();
    const spawned = await driver.spawn(spawnParams(openNetwork));
    const token = spawned.env?.ATTN_PI_TOKEN ?? "";
    const credentials = spawned.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";
    const suite = await FakeSuite.connect(socketPath, token);

    await driver.policyChanged({ network: { ...openNetwork, denied_domains: ["127.0.0.1"] } });
    const response = await proxyConnect(spawned.env?.ATTN_PI_PROXY_ADDR ?? "", credentials, "127.0.0.1", upstream);

    expect(response).toContain('Network access to "127.0.0.1" is blocked by policy.');
    expect(suite.asked).toEqual([]);
  });

  test("a restarted driver recovers active command identities from the live suite", async () => {
    const upstream = await startUpstream();
    const first = await buildDriver();
    const spawned = await first.driver.spawn(spawnParams(openNetwork));
    const token = spawned.env!.ATTN_PI_TOKEN;
    const address = spawned.env!.ATTN_PI_PROXY_ADDR;
    const proxy = { host: "127.0.0.1" as const, port: Number(address.split(":")[1]), credentials: spawned.env!.ATTN_PI_PROXY_CREDENTIALS };
    const suite = new AttnPiSuite({ socketPath: first.socketPath, token, piVersion: "0.83.0", proxyCredentials: proxy.credentials });
    cleanups.push(() => suite.close());
    const pi = new FakePi();
    suite.register(pi as never);
    pi.fire("session_start", { type: "session_start", reason: "startup" }, new FakeContext("pi-1"));
    const command = await suite.acquireCommandProxy(proxy);
    const seen: NetworkRequest[] = [];
    suite.networkDecider = async (request) => { seen.push(request); return { decision: "allow" }; };
    await first.driver.close();
    first.relay.close();

    const second = await buildDriver({
      socketPath: first.socketPath,
      stateDir: first.stateDir,
      initialize: true,
      registerResult: {
        ok: true,
        auto_mode: { enabled_default: true, network: openNetwork },
        active_runs: [{ session_id: "session-1", run_id: "run-1", seq: 0,
          metadata: { schema: 1, pi_session_id: "pi-1", pi_version: "0.83.0" } }],
      },
    });
    const reconnect = await suite.acquireCommandProxy(proxy);
    expect(second.driver.networkProxy()!.knowsCredentials(proxy.credentials)).toBe(true);
    const response = await proxyConnect(address, command.proxy.credentials, "127.0.0.1", upstream);
    expect(response).toContain("200 Connection established");
    expect(seen).toEqual([{ credentials: command.proxy.credentials, host: "127.0.0.1", port: upstream, protocol: "https_connect" }]);
    command.release();
    const drain = await suite.acquireCommandProxy(proxy);
    await second.driver.suiteReportProxyCommands({ token, proxy_commands: { revision: 1, credentials: [command.proxy.credentials] } });
    expect(await proxyConnect(address, command.proxy.credentials, "127.0.0.1", upstream)).toContain("407");
    reconnect.release();
    drain.release();
  });

  test("relaunching a session revokes the credentials and grants of the run it replaces", async () => {
    const upstream = await startUpstream();
    const { driver } = await buildDriver();
    const first = await driver.spawn(spawnParams(openNetwork));
    const stale = first.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";
    const address = first.env?.ATTN_PI_PROXY_ADDR ?? "";
    driver.networkProxy()?.allowHost(stale, "127.0.0.1", "session");

    const second = await driver.spawn(spawnParams(openNetwork, { run_id: "run-2" }));
    const fresh = second.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";

    expect(fresh).not.toBe(stale);
    expect(await proxyConnect(address, stale, "127.0.0.1", upstream)).toContain("407");
    // The successor starts with no grants of its own, so its first call still asks.
    expect(driver.networkProxy()?.evaluateHost(fresh, "127.0.0.1")).toBe("ask");
  });

  test("a late close for a replaced run leaves the successor's credentials alone", async () => {
    const { driver } = await buildDriver();
    await driver.spawn(spawnParams(openNetwork));
    const second = await driver.spawn(spawnParams(openNetwork, { run_id: "run-2" }));
    const fresh = second.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";

    // attn reports the first run's close after the relaunch already happened.
    await driver.sessionClosed({ session_id: "session-1", run_id: "run-1", reason: "reloaded" });

    expect(driver.networkProxy()?.evaluateHost(fresh, "example.com")).toBe("ask");
    expect(driver.networkProxy()?.knowsCredentials(fresh)).toBe(true);
  });

  test("a register that carries no auto_mode starts no proxy", async () => {
    const { driver } = await buildDriver({ initialize: true, registerResult: { ok: true, active_runs: [] } });

    expect(driver.networkProxy()).toBeUndefined();
  });

  test("closing a session revokes its proxy credentials", async () => {
    const upstream = await startUpstream();
    const { driver } = await buildDriver();
    const spawned = await driver.spawn(spawnParams(openNetwork));
    const credentials = spawned.env?.ATTN_PI_PROXY_CREDENTIALS ?? "";

    await driver.sessionClosed({ session_id: "session-1", run_id: "run-1", reason: "exit" });
    const response = await proxyConnect(spawned.env?.ATTN_PI_PROXY_ADDR ?? "", credentials, "127.0.0.1", upstream);

    expect(response).toContain("407 Proxy Authentication Required");
  });
});

class FakeContext implements ExtensionContextLike {
  constructor(private readonly sessionId: string) {}

  isIdle(): boolean {
    return true;
  }

  get sessionManager(): SessionManagerLike {
    const sessionId = this.sessionId;
    return { getSessionId: () => sessionId };
  }
}

class FakePi {
  readonly handlers = new Map<string, ExtensionHandler<never>>();

  on(event: string, handler: ExtensionHandler<never>): void {
    this.handlers.set(event, handler);
  }

  sendUserMessage(): void {}

  fire(eventType: string, event: unknown, ctx: ExtensionContextLike): void {
    void this.handlers.get(eventType)?.(event as never, ctx);
  }
}

class CollectingDelegate implements RelayDelegate {
  readonly connections: RelayConnection[] = [];
  readonly firstConnection: Promise<RelayConnection>;
  onProxyCommands: ((params: { proxy_commands: { credentials: string[] } }) => Promise<void>) | undefined;
  private announce: (connection: RelayConnection) => void = () => {};

  constructor() {
    this.firstConnection = new Promise<RelayConnection>((resolve) => {
      this.announce = resolve;
    });
  }

  async suiteHello(connection: RelayConnection): Promise<{ ok: true }> {
    this.connections.push(connection);
    this.announce(connection);
    return { ok: true };
  }

  async suiteReportState(): Promise<void> {}
  async suiteReportProxyCommands(params: { proxy_commands: { credentials: string[] } }): Promise<void> {
    await this.onProxyCommands?.(params);
  }
  async suiteReportStop(): Promise<void> {}
  async suiteReportDenial(): Promise<void> {}
  async suiteReportInputTaken(): Promise<void> {}
  async suiteReportPullRequest(): Promise<void> {}
  async suiteReportExecPolicyAmendment(): Promise<void> {}
  async suiteReportNetworkAmendment(): Promise<void> {}
}

async function connectedSuite(
  token: string,
  proxyCredentials?: string,
): Promise<{ suite: AttnPiSuite; connection: RelayConnection; delegate: CollectingDelegate }> {
  const socketPath = nextSocketPath();
  const delegate = new CollectingDelegate();
  const relay = new RelayServer({ socketPath, delegate });
  await relay.listen();
  const suite = new AttnPiSuite({ socketPath, token, piVersion: "0.80.10", proxyCredentials });
  cleanups.push(() => {
    suite.close();
    relay.close();
  });
  const pi = new FakePi();
  suite.register(pi as never);
  pi.fire("session_start", { type: "session_start", reason: "startup" }, new FakeContext("pi-1"));
  return { suite, connection: await delegate.firstConnection, delegate };
}

describe("the suite answers driver.network_decide", () => {
  test("an unset decider denies", async () => {
    const { connection } = await connectedSuite("run-token-1");

    const decision = await connection.request<RelayNetworkDecideResult>(relayMethods.networkDecide, {
      credentials: "command-creds-1",
      host: "example.com",
      port: 443,
      protocol: "https_connect",
    });

    expect(decision).toEqual({ decision: "deny" });
  });

  test("a set decider receives the requesting command credentials unchanged", async () => {
    const { suite, connection } = await connectedSuite("run-token-1", "proxy-creds-1");
    const seen: NetworkRequest[] = [];
    suite.networkDecider = async (request) => {
      seen.push(request);
      return { decision: "allow", scope: "session" };
    };

    const decision = await connection.request<RelayNetworkDecideResult>(relayMethods.networkDecide, {
      credentials: "command-creds-1",
      host: "example.com",
      port: 443,
      protocol: "https_connect",
    });

    expect(decision).toEqual({ decision: "allow", scope: "session" });
    expect(seen).toEqual([{ credentials: "command-creds-1", host: "example.com", port: 443, protocol: "https_connect" }]);
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

test("parallel commands retain their proxy identity and queue only their reviews", async () => {
  const { ApprovalOrchestrator } = await import("../approval/orchestrator");
  const upstream = await startUpstream();
  const { driver, socketPath } = await buildDriver();
  const spawned = await driver.spawn(spawnParams(openNetwork));
  const address = spawned.env!.ATTN_PI_PROXY_ADDR;
  const proxy = { host: "127.0.0.1" as const, port: Number(address.split(":")[1]), credentials: spawned.env!.ATTN_PI_PROXY_CREDENTIALS };
  const suite = new AttnPiSuite({ socketPath, token: spawned.env!.ATTN_PI_TOKEN, piVersion: "0.83.0", proxyCredentials: proxy.credentials });
  cleanups.push(() => suite.close());
  const pi = new FakePi();
  suite.register(pi as never);
  pi.fire("session_start", { type: "session_start", reason: "startup" }, new FakeContext("pi-1"));
  const started = [deferred<string>(), deferred<string>(), deferred<string>()];
  const finished = started.map(() => deferred<{ exitCode: number | null }>());
  const firstReview = deferred<void>();
  const secondNetworkRequest = deferred<void>();
  const thirdNetworkRequest = deferred<void>();
  const answer = deferred<void>();
  const reviewed: string[] = [];
  const aborted: number[] = [];
  let output = "";
  const orchestrator = new ApprovalOrchestrator({
    approvalPolicy: () => "on-request", sandboxMode: () => "read-only", rules: [], proxy,
    acquireProxy: (signal) => suite.acquireCommandProxy(proxy, signal),
    sandbox: () => ({ config: { mode: "read-only", network: "proxy", allowWrite: [], denyRead: [], denyWrite: [], cacheWritePaths: [] }, cwd: tmpRoot, temp: tmpRoot }),
    reviewer: () => ({ name: "user", review: async (request) => {
      if (request.kind !== "network") throw new Error("unexpected command review");
      reviewed.push(request.trigger!.command);
      if (reviewed.length === 1) { firstReview.resolve(); await answer.promise; return { type: "denied", rejection: "no" }; }
      return { type: "approved_for_session" };
    } }),
    run: (command, _cwd, options) => {
      const index = command.includes("command-A") ? 0 : command.includes("command-B") ? 1 : 2;
      options.signal!.addEventListener("abort", () => { aborted.push(index); finished[index]!.resolve({ exitCode: null }); }, { once: true });
      const credentials = decodeURIComponent(new URL(options.env!.HTTPS_PROXY!).username);
      options.onData(Buffer.from(credentials.slice(0, 8)));
      options.onData(Buffer.from(credentials.slice(8) + "\n"));
      started[index]!.resolve(credentials);
      return finished[index]!.promise;
    },
  });
  let networkRequests = 0;
  suite.networkDecider = (request) => {
    networkRequests += 1;
    if (networkRequests === 2) secondNetworkRequest.resolve();
    if (networkRequests === 3) thirdNetworkRequest.resolve();
    return orchestrator.decideNetwork(request);
  };
  const ctx = { cwd: tmpRoot, onData: (data: Buffer) => { output += data.toString(); } };
  const a = orchestrator.runBash({ command: "echo command-A" }, { ...ctx, toolCallId: "a" }).catch((error: Error) => error.message);
  const b = orchestrator.runBash({ command: "echo command-B" }, { ...ctx, toolCallId: "b" });
  const c = orchestrator.runBash({ command: "echo command-C" }, { ...ctx, toolCallId: "c" });
  const [credentialsA, credentialsB, credentialsC] = await Promise.all(started.map((item) => item.promise));
  expect(new Set([credentialsA, credentialsB, credentialsC]).size).toBe(3);
  const requestA = proxyConnect(address, credentialsA!, "127.0.0.1", upstream);
  await firstReview.promise;
  const requestB = proxyConnect(address, credentialsB!, "127.0.0.1", upstream);
  await secondNetworkRequest.promise;
  const requestC = proxyConnect(address, credentialsC!, "127.0.0.1", upstream);
  await thirdNetworkRequest.promise;
  expect(reviewed).toEqual(["echo command-A"]);
  answer.resolve();
  expect(await requestA).toContain("403");
  expect(await a).toContain("was blocked by policy");
  expect(aborted).toEqual([0]);
  expect(await requestB).toContain("200 Connection established");
  expect(await requestC).toContain("200 Connection established");
  expect(reviewed).toEqual(["echo command-A", "echo command-B"]);
  finished[1]!.resolve({ exitCode: 0 });
  expect(await b).toEqual({ exitCode: 0 });
  finished[2]!.resolve({ exitCode: 0 });
  expect(await c).toEqual({ exitCode: 0 });
  const drain = await suite.acquireCommandProxy(proxy);
  expect(driver.networkProxy()!.knowsCredentials(credentialsA!)).toBe(false);
  expect(driver.networkProxy()!.knowsCredentials(credentialsB!)).toBe(false);
  expect(driver.networkProxy()!.knowsCredentials(credentialsC!)).toBe(false);
  drain.release();
  expect(output).toBe("[REDACTED credential]\n".repeat(3));
  expect(await orchestrator.decideNetwork({ credentials: credentialsA!, host: "127.0.0.1", port: upstream, protocol: "https_connect" })).toEqual({ decision: "deny" });
});

test("cancelling command registration revokes its identity without waiting for the driver reply", async () => {
  const { suite, delegate } = await connectedSuite("run-token-1", "bootstrap-credentials");
  const registered = deferred<void>();
  const released = deferred<void>();
  const reply = deferred<void>();
  delegate.onProxyCommands = async (params) => {
    if (params.proxy_commands.credentials.length > 0) { registered.resolve(); await reply.promise; }
    else released.resolve();
  };
  const controller = new AbortController();
  const command = suite.acquireCommandProxy({ host: "127.0.0.1", port: 1, credentials: "bootstrap-credentials" }, controller.signal);
  await registered.promise;
  controller.abort(new Error("turn cancelled"));
  await expect(command).rejects.toThrow("turn cancelled");
  await released.promise;
  reply.resolve();
});
