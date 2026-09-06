import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync } from "node:fs";
import { createConnection, createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NetworkProxy } from "../netproxy";
import type { Decider, NetworkDecision, NetworkPolicy, NetworkRequest } from "../netproxy";

const token = "run-token-1";
const otherToken = "run-token-2";

const cleanups: Array<() => Promise<void> | void> = [];

afterEach(async () => {
  while (cleanups.length > 0) await cleanups.pop()?.();
});

/** A real TCP upstream: it records the first thing it is sent and answers one HTTP response. */
type Upstream = { port: number; received: string[] };

async function startUpstream(): Promise<Upstream> {
  const received: string[] = [];
  const server: Server = createServer((socket) => {
    socket.once("data", (chunk: Buffer) => {
      received.push(chunk.toString("utf8"));
      socket.end("HTTP/1.1 200 OK\r\ncontent-length: 8\r\nconnection: close\r\n\r\nUPSTREAM");
    });
  });
  await new Promise<void>((resolve) => server.listen({ host: "127.0.0.1", port: 0 }, () => resolve()));
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("upstream has no TCP address");
  cleanups.push(() => new Promise<void>((resolve) => server.close(() => resolve())));
  return { port: address.port, received };
}

class ScriptedDecider {
  readonly calls: NetworkRequest[] = [];
  answer: (request: NetworkRequest) => Promise<NetworkDecision> = async () => ({ decision: "deny" });
  onCall: (() => void) | undefined;

  readonly decide: Decider = async (request) => {
    this.calls.push(request);
    this.onCall?.();
    return this.answer(request);
  };
}

type Harness = { proxy: NetworkProxy; port: number; decider: ScriptedDecider; stateDir: string };

async function startProxy(policy: Partial<NetworkPolicy>, credentials: string[] = [token]): Promise<Harness> {
  const decider = new ScriptedDecider();
  const stateDir = mkdtempSync(join(tmpdir(), "attn-netproxy-"));
  const proxy = new NetworkProxy({
    policy: { enabled: true, allowed_domains: [], denied_domains: [], ...policy },
    stateDir,
    decide: decider.decide,
  });
  const address = await proxy.listen();
  cleanups.push(() => proxy.close());
  for (const entry of credentials) proxy.registerCredentials(entry);
  return { proxy, port: address.port, decider, stateDir };
}

/** A byte-level client for the proxy, written against the wire rather than the proxy's own reader. */
class Wire {
  private buffer: Buffer = Buffer.alloc(0);
  private wake: (() => void) | undefined;
  private closed = false;

  private constructor(private readonly socket: Socket) {
    socket.on("data", (chunk: Buffer) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      this.notify();
    });
    socket.on("close", () => {
      this.closed = true;
      this.notify();
    });
    socket.on("error", () => {
      this.closed = true;
      this.notify();
    });
  }

  static async open(port: number): Promise<Wire> {
    const socket = await new Promise<Socket>((resolve, reject) => {
      const candidate = createConnection({ host: "127.0.0.1", port });
      candidate.once("error", reject);
      candidate.once("connect", () => {
        candidate.off("error", reject);
        resolve(candidate);
      });
    });
    const wire = new Wire(socket);
    cleanups.push(() => {
      socket.destroy();
    });
    return wire;
  }

  get buffered(): number {
    return this.buffer.length;
  }

  write(data: string | Buffer): void {
    this.socket.write(data);
  }

  async read(count: number): Promise<number[]> {
    while (this.buffer.length < count) {
      if (this.closed) throw new Error(`the proxy closed before sending ${count} bytes`);
      await this.tick();
    }
    const head = this.buffer.subarray(0, count);
    this.buffer = this.buffer.subarray(count);
    return [...head];
  }

  async readUntil(needle: string): Promise<string> {
    for (;;) {
      const at = this.buffer.indexOf(needle);
      if (at >= 0) {
        const head = this.buffer.subarray(0, at + needle.length).toString("utf8");
        this.buffer = this.buffer.subarray(at + needle.length);
        return head;
      }
      if (this.closed) throw new Error(`the proxy closed before sending ${JSON.stringify(needle)}`);
      await this.tick();
    }
  }

  async readToClose(): Promise<string> {
    while (!this.closed) await this.tick();
    return this.buffer.toString("utf8");
  }

  private tick(): Promise<void> {
    return new Promise<void>((resolve) => {
      this.wake = resolve;
    });
  }

  private notify(): void {
    const wake = this.wake;
    this.wake = undefined;
    wake?.();
  }
}

function basic(credentials: string): string {
  return Buffer.from(`${credentials}:`, "utf8").toString("base64");
}

function connectRequest(host: string, port: number, credentials?: string): string {
  const auth = credentials === undefined ? "" : `Proxy-Authorization: Basic ${basic(credentials)}\r\n`;
  return `CONNECT ${host}:${port} HTTP/1.1\r\nHost: ${host}:${port}\r\n${auth}\r\n`;
}

function absoluteRequest(host: string, port: number, path: string, credentials: string): string {
  return [
    `GET http://${host}:${port}${path} HTTP/1.1`,
    `Host: ${host}:${port}`,
    `Proxy-Authorization: Basic ${basic(credentials)}`,
    "Proxy-Connection: keep-alive",
    "",
    "",
  ].join("\r\n");
}

function portBytes(port: number): Buffer {
  const bytes = Buffer.alloc(2);
  bytes.writeUInt16BE(port, 0);
  return bytes;
}

async function socks5Handshake(wire: Wire, credentials: string): Promise<void> {
  wire.write(Buffer.from([0x05, 0x01, 0x02]));
  expect(await wire.read(2)).toEqual([0x05, 0x02]);
  const username = Buffer.from(credentials, "utf8");
  wire.write(Buffer.concat([Buffer.from([0x01, username.length]), username, Buffer.from([0x00])]));
}

async function socks5Connect(wire: Wire, host: string, port: number): Promise<number> {
  const name = Buffer.from(host, "utf8");
  wire.write(Buffer.concat([Buffer.from([0x05, 0x01, 0x00, 0x03, name.length]), name, portBytes(port)]));
  const reply = await wire.read(10);
  return reply[1] ?? -1;
}

// Lets every already-scheduled task run, so "the client has heard nothing yet" is a
// statement about a drained event loop rather than about timing.
async function drainEventLoop(): Promise<void> {
  for (let round = 0; round < 3; round++) await new Promise<void>((resolve) => setImmediate(resolve));
}

describe("network proxy over the wire", () => {
  test("an allowlisted host is reachable through CONNECT without asking the decider", async () => {
    const upstream = await startUpstream();
    const { port, decider } = await startProxy({ allowed_domains: ["127.0.0.1"] });
    const wire = await Wire.open(port);

    wire.write(connectRequest("127.0.0.1", upstream.port, token));
    expect(await wire.readUntil("\r\n\r\n")).toContain("200 Connection established");
    wire.write("ping\n");

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(decider.calls).toEqual([]);
  });

  test("an allowlisted host is reachable through plain HTTP, rewritten to origin form", async () => {
    const upstream = await startUpstream();
    const { port } = await startProxy({ allowed_domains: ["127.0.0.1"] });
    const wire = await Wire.open(port);

    wire.write(absoluteRequest("127.0.0.1", upstream.port, "/hello", token));

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(upstream.received[0]).toContain(`GET /hello HTTP/1.1`);
    expect(upstream.received[0]).not.toContain("Proxy-Authorization");
    expect(upstream.received[0]).not.toContain("Proxy-Connection");
  });

  test("an allowlisted host is reachable through SOCKS5", async () => {
    const upstream = await startUpstream();
    const { port, decider } = await startProxy({ allowed_domains: ["127.0.0.1"] });
    const wire = await Wire.open(port);

    await socks5Handshake(wire, token);
    expect(await wire.read(2)).toEqual([0x01, 0x00]);
    expect(await socks5Connect(wire, "127.0.0.1", upstream.port)).toBe(0x00);
    wire.write("ping\n");

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(decider.calls).toEqual([]);
  });

  test("a denied-rule host gets the 403 body and a recorded denial, and the decider is never asked", async () => {
    const { port, decider, proxy } = await startProxy({
      allowed_domains: ["**.example.com"],
      denied_domains: ["blocked.example.com"],
    });
    const wire = await Wire.open(port);

    wire.write(connectRequest("blocked.example.com", 443, token));
    const response = await wire.readToClose();

    expect(response).toContain("403 Forbidden");
    expect(response).toContain('Network access to "blocked.example.com" is blocked by policy.');
    expect(decider.calls).toEqual([]);
    const denials = proxy.denials(token);
    expect(denials).toHaveLength(1);
    expect(denials[0]).toMatchObject({
      credentials: token,
      host: "blocked.example.com",
      port: 443,
      protocol: "https_connect",
      reason: "denied",
    });
  });

  test("a deny rule wins over an allow rule for the same host", async () => {
    const { proxy } = await startProxy({
      allowed_domains: ["**.example.com"],
      denied_domains: ["*.example.com"],
    });

    expect(proxy.evaluateHost(token, "api.example.com")).toBe("deny");
    expect(proxy.evaluateHost(token, "example.com")).toBe("allow");
  });

  test("local and private addresses are hard denied even under a global allowlist", async () => {
    const upstream = await startUpstream();
    const { port, decider, proxy } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    wire.write(connectRequest("127.0.0.1", upstream.port, token));
    const response = await wire.readToClose();

    expect(response).toContain("403 Forbidden");
    expect(decider.calls).toEqual([]);
    expect(proxy.denials(token)[0]?.reason).toBe("not_allowed_local");
    for (const host of ["localhost", "10.0.0.5", "192.168.1.9", "169.254.1.1", "::1", "100.64.0.1"]) {
      expect(proxy.evaluateHost(token, host)).toBe("deny");
    }
    expect(proxy.evaluateHost(token, "8.8.8.8")).toBe("allow");
  });

  // `127.1` is the ask-path fixture: the literal classifier does not read it as an address,
  // so it reaches the decider, and the OS still resolves it to the local upstream.
  test("an allowlist miss holds the connection on the decider, then proceeds once it allows", async () => {
    const upstream = await startUpstream();
    const { port, decider } = await startProxy({ allowed_domains: ["nothing.example.com"] });
    const wire = await Wire.open(port);
    let release: (decision: NetworkDecision) => void = () => {};
    decider.answer = () => new Promise<NetworkDecision>((resolve) => (release = resolve));
    const asked = new Promise<void>((resolve) => (decider.onCall = resolve));

    wire.write(connectRequest("127.1", upstream.port, token));
    await asked;
    await drainEventLoop();
    expect(wire.buffered).toBe(0);

    release({ decision: "allow" });
    expect(await wire.readUntil("\r\n\r\n")).toContain("200 Connection established");
    expect(decider.calls).toHaveLength(1);
    expect(decider.calls[0]).toMatchObject({
      credentials: token,
      host: "127.1",
      port: upstream.port,
      protocol: "https_connect",
    });
  });

  test("a decider that denies gets the 403 body and a recorded denial", async () => {
    const { port, decider, proxy } = await startProxy({ allowed_domains: [] });
    decider.answer = async () => ({ decision: "deny" });
    const wire = await Wire.open(port);

    wire.write(connectRequest("api.example.com", 443, token));
    const response = await wire.readToClose();

    expect(response).toContain('Network access to "api.example.com" is blocked by policy.');
    expect(decider.calls).toHaveLength(1);
    expect(proxy.denials(token)[0]?.reason).toBe("not_allowed");
  });

  test("a decider that throws denies with decider_unavailable", async () => {
    const { port, decider, proxy } = await startProxy({ allowed_domains: [] });
    decider.answer = async () => {
      throw new Error("the suite is gone");
    };
    const wire = await Wire.open(port);

    wire.write(connectRequest("api.example.com", 443, token));

    expect(await wire.readToClose()).toContain("403 Forbidden");
    expect(proxy.denials(token)[0]?.reason).toBe("decider_unavailable");
  });

  test("a session-scoped grant lets the next connection skip the decider", async () => {
    const upstream = await startUpstream();
    const { port, decider } = await startProxy({ allowed_domains: [] });
    decider.answer = async () => ({ decision: "allow", scope: "session" });

    const first = await Wire.open(port);
    first.write(connectRequest("127.1", upstream.port, token));
    expect(await first.readUntil("\r\n\r\n")).toContain("200 Connection established");

    const second = await Wire.open(port);
    second.write(connectRequest("127.1", upstream.port, token));
    expect(await second.readUntil("\r\n\r\n")).toContain("200 Connection established");

    expect(decider.calls).toHaveLength(1);
  });

  test("a once-scoped grant is spent by one connection and the next one asks again", async () => {
    const upstream = await startUpstream();
    const { port, decider, proxy } = await startProxy({ allowed_domains: [] });
    decider.answer = async () => ({ decision: "deny" });
    proxy.allowHost(token, "127.1", "once");

    const first = await Wire.open(port);
    first.write(connectRequest("127.1", upstream.port, token));
    expect(await first.readUntil("\r\n\r\n")).toContain("200 Connection established");
    expect(decider.calls).toEqual([]);

    const second = await Wire.open(port);
    second.write(connectRequest("127.1", upstream.port, token));
    expect(await second.readToClose()).toContain("403 Forbidden");
    expect(decider.calls).toHaveLength(1);
  });

  test("revoking credentials forgets their grants and refuses their next connection", async () => {
    const upstream = await startUpstream();
    const { port, proxy, decider } = await startProxy({ allowed_domains: [] });
    proxy.allowHost(token, "127.1", "session");
    expect(proxy.evaluateHost(token, "127.1")).toBe("allow");

    proxy.revokeCredentials(token);
    proxy.registerCredentials(token);
    expect(proxy.evaluateHost(token, "127.1")).toBe("ask");

    decider.answer = async () => ({ decision: "deny" });
    const wire = await Wire.open(port);
    wire.write(connectRequest("127.1", upstream.port, token));
    expect(await wire.readToClose()).toContain("403 Forbidden");
  });

  test("a grant belongs to the credentials that earned it", async () => {
    const { proxy } = await startProxy({ allowed_domains: [] }, [token, otherToken]);
    proxy.allowHost(token, "127.1", "session");

    expect(proxy.evaluateHost(token, "127.1")).toBe("allow");
    expect(proxy.evaluateHost(otherToken, "127.1")).toBe("ask");
  });

  test("wildcard rules follow Codex's shapes", async () => {
    const { proxy } = await startProxy({ allowed_domains: ["*.wild.test", "**.both.test", "exact.test"] });

    expect(proxy.evaluateHost(token, "api.wild.test")).toBe("allow");
    expect(proxy.evaluateHost(token, "deep.api.wild.test")).toBe("allow");
    expect(proxy.evaluateHost(token, "wild.test")).toBe("ask");
    expect(proxy.evaluateHost(token, "both.test")).toBe("allow");
    expect(proxy.evaluateHost(token, "api.both.test")).toBe("allow");
    expect(proxy.evaluateHost(token, "EXACT.test.")).toBe("allow");
    expect(proxy.evaluateHost(token, "sub.exact.test")).toBe("ask");
  });

  test("an HTTP connection with no credentials is refused before any policy check", async () => {
    const { port, decider, proxy } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    wire.write(connectRequest("api.example.com", 443));
    const response = await wire.readToClose();

    expect(response).toContain("407 Proxy Authentication Required");
    expect(decider.calls).toEqual([]);
    expect(proxy.denials("")[0]?.reason).toBe("no_credentials");
  });

  test("an HTTP connection with unknown credentials is refused", async () => {
    const { port, proxy } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    wire.write(connectRequest("api.example.com", 443, "not-a-run-token"));

    expect(await wire.readToClose()).toContain("407 Proxy Authentication Required");
    expect(proxy.denials("not-a-run-token")[0]?.reason).toBe("no_credentials");
  });

  test("a SOCKS5 client offering no authentication is refused", async () => {
    const { port } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    wire.write(Buffer.from([0x05, 0x01, 0x00]));

    expect(await wire.read(2)).toEqual([0x05, 0xff]);
  });

  test("a SOCKS5 client with unknown credentials fails authentication", async () => {
    const { port } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    await socks5Handshake(wire, "not-a-run-token");

    expect(await wire.read(2)).toEqual([0x01, 0x01]);
  });

  test("a SOCKS5 denial answers reply 0x02 and records the denial", async () => {
    const { port, proxy } = await startProxy({
      allowed_domains: ["**.example.com"],
      denied_domains: ["blocked.example.com"],
    });
    const wire = await Wire.open(port);

    await socks5Handshake(wire, token);
    expect(await wire.read(2)).toEqual([0x01, 0x00]);

    expect(await socks5Connect(wire, "blocked.example.com", 443)).toBe(0x02);
    expect(proxy.denials(token)[0]).toMatchObject({ protocol: "socks5_tcp", reason: "denied" });
  });

  test("denials since a timestamp exclude what came before it", async () => {
    const { proxy } = await startProxy({ denied_domains: ["*.example.com"] });
    const request = { credentials: token, host: "a.example.com", port: 443, protocol: "http" } as const;

    proxy.recordDenial(request, "denied");
    const mark = proxy.denials(token)[0]?.at ?? "";
    await new Promise<void>((resolve) => setTimeout(resolve, 2));
    proxy.recordDenial({ ...request, host: "b.example.com" }, "denied");

    expect(proxy.denials(token, mark).map((entry) => entry.host)).toEqual(["b.example.com"]);
  });

  test("a disabled policy denies every host", async () => {
    const { proxy } = await startProxy({ enabled: false, allowed_domains: ["*"] });

    expect(proxy.evaluateHost(token, "example.com")).toBe("deny");
  });

  test("setPolicy replaces the rules a live proxy enforces", async () => {
    const { proxy } = await startProxy({ allowed_domains: ["example.com"] });
    expect(proxy.evaluateHost(token, "example.com")).toBe("allow");

    proxy.setPolicy({ enabled: true, allowed_domains: ["other.test"], denied_domains: ["example.com"] });

    expect(proxy.evaluateHost(token, "example.com")).toBe("deny");
    expect(proxy.evaluateHost(token, "other.test")).toBe("allow");
  });

  test("the chosen port is persisted and reused across a restart", async () => {
    const { proxy, port, stateDir } = await startProxy({ allowed_domains: [] });

    expect(JSON.parse(readFileSync(join(stateDir, "proxy.json"), "utf8"))).toEqual({ port });
    await proxy.close();
    const again = await proxy.listen();

    expect(again).toEqual({ host: "127.0.0.1", port });
  });
});
