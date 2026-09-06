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

  async request(method: string, params: unknown): Promise<unknown> {
    this.requests.push({ method, params });
    if (method === "driver.register") return { ok: true, active_runs: [] };
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

async function buildDriver(): Promise<{ driver: PiDriver; socketPath: string }> {
  const socketPath = nextSocketPath();
  let driver: PiDriver;
  const relay = new RelayServer({
    socketPath,
    delegate: {
      suiteHello: (connection: RelayConnection, params: unknown) => driver.suiteHello(connection, params),
      suiteReportState: (params: unknown) => driver.suiteReportState(params),
      suiteReportStop: (params: unknown) => driver.suiteReportStop(params),
      suiteReportDenial: (params: unknown) => driver.suiteReportDenial(params),
      suiteReportInputTaken: (params: unknown) => driver.suiteReportInputTaken(params),
      suiteReportPullRequest: (params: unknown) => driver.suiteReportPullRequest(params),
    },
  });
  driver = new PiDriver({
    rpc: new FakeRPC() as never,
    relay,
    suitePath,
    runCommand: fakeRunCommand,
    env: {},
    proxyStateDir: mkdtempSync(join(tmpdir(), "attn-np-state-")),
  });
  await relay.listen();
  cleanups.push(async () => {
    await driver.close();
    relay.close();
  });
  return { driver, socketPath };
}

// Loopback is where the test upstream lives, so these spawns opt into local binding.
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

/** A suite that answers driver.network_decide with whatever the test scripts. */
class FakeSuite {
  private buffer = "";
  readonly asked: Array<{ method: string; params: unknown }> = [];
  answer: unknown = { decision: "allow" };

  private constructor(private readonly socket: Socket) {
    socket.setEncoding("utf8");
    socket.on("data", (chunk: string) => this.consume(chunk));
  }

  static async connect(socketPath: string, token: string): Promise<FakeSuite> {
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
        params: { token, pi_session_id: "pi-1", pi_version: "0.80.10", reason: "startup" },
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
      if (message.method === undefined) continue;
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
    // The sandboxed command carries the proxy credentials; the relay token must stay
    // out of its reach, so the two are never the same string.
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
      { method: relayMethods.networkDecide, params: { host: "127.0.0.1", port: upstream, protocol: "https_connect" } },
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
  /** Resolves on the first suite.hello, so tests wait on the handshake itself. */
  readonly firstConnection: Promise<RelayConnection>;
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
  async suiteReportStop(): Promise<void> {}
  async suiteReportDenial(): Promise<void> {}
  async suiteReportInputTaken(): Promise<void> {}
  async suiteReportPullRequest(): Promise<void> {}
}

async function connectedSuite(
  token: string,
  proxyCredentials?: string,
): Promise<{ suite: AttnPiSuite; connection: RelayConnection }> {
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
  return { suite, connection: await delegate.firstConnection };
}

describe("the suite answers driver.network_decide", () => {
  test("an unset decider denies", async () => {
    const { connection } = await connectedSuite("run-token-1");

    const decision = await connection.request<RelayNetworkDecideResult>(relayMethods.networkDecide, {
      host: "example.com",
      port: 443,
      protocol: "https_connect",
    });

    expect(decision).toEqual({ decision: "deny" });
  });

  test("a set decider is called with the run's proxy credentials, not its relay token", async () => {
    const { suite, connection } = await connectedSuite("run-token-1", "proxy-creds-1");
    const seen: NetworkRequest[] = [];
    suite.networkDecider = async (request) => {
      seen.push(request);
      return { decision: "allow", scope: "session" };
    };

    const decision = await connection.request<RelayNetworkDecideResult>(relayMethods.networkDecide, {
      host: "example.com",
      port: 443,
      protocol: "https_connect",
    });

    expect(decision).toEqual({ decision: "allow", scope: "session" });
    expect(seen).toEqual([{ credentials: "proxy-creds-1", host: "example.com", port: 443, protocol: "https_connect" }]);
  });
});
