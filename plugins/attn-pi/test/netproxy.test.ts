import { afterEach, describe, expect, jest, test } from "bun:test";
import { mkdtempSync, readFileSync } from "node:fs";
import { createConnection, createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { NetworkProxy } from "../netproxy";
import { SocketReader } from "../netproxy/stream";
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

// The upstream these tests dial is on loopback, so the harness opts into local binding
// the way Codex's own tests do; the cases about the local guard turn it back off.
async function startProxy(
  policy: Partial<NetworkPolicy>,
  credentials: string[] = [token],
  lookup?: (host: string) => Promise<string[]>,
): Promise<Harness> {
  const decider = new ScriptedDecider();
  const stateDir = mkdtempSync(join(tmpdir(), "attn-netproxy-"));
  const proxy = new NetworkProxy({
    policy: { enabled: true, allowed_domains: [], denied_domains: [], allow_local_binding: true, ...policy },
    stateDir,
    decide: decider.decide,
    ...(lookup ? { lookup } : {}),
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

  /** Bytes this client has handed the kernel, and bytes still queued in Node. */
  get progress(): { sent: number; queued: number } {
    return { sent: this.socket.bytesWritten, queued: this.socket.writableLength };
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
    const { port, decider, proxy } = await startProxy({ allowed_domains: ["*"], allow_local_binding: false });
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

  test("an allowlist miss holds the connection on the decider, then proceeds once it allows", async () => {
    const upstream = await startUpstream();
    const { port, decider } = await startProxy({ allowed_domains: ["nothing.example.com"] });
    const wire = await Wire.open(port);
    let release: (decision: NetworkDecision) => void = () => {};
    decider.answer = () => new Promise<NetworkDecision>((resolve) => (release = resolve));
    const asked = new Promise<void>((resolve) => (decider.onCall = resolve));

    wire.write(connectRequest("127.0.0.1", upstream.port, token));
    await asked;
    await drainEventLoop();
    expect(wire.buffered).toBe(0);

    release({ decision: "allow" });
    expect(await wire.readUntil("\r\n\r\n")).toContain("200 Connection established");
    expect(decider.calls).toHaveLength(1);
    expect(decider.calls[0]).toMatchObject({
      credentials: token,
      host: "127.0.0.1",
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
    first.write(connectRequest("127.0.0.1", upstream.port, token));
    expect(await first.readUntil("\r\n\r\n")).toContain("200 Connection established");

    const second = await Wire.open(port);
    second.write(connectRequest("127.0.0.1", upstream.port, token));
    expect(await second.readUntil("\r\n\r\n")).toContain("200 Connection established");

    expect(decider.calls).toHaveLength(1);
  });

  test("a once-scoped grant is spent by one connection and the next one asks again", async () => {
    const upstream = await startUpstream();
    const { port, decider, proxy } = await startProxy({ allowed_domains: [] });
    decider.answer = async () => ({ decision: "deny" });
    proxy.allowHost(token, "127.0.0.1", "once");

    const first = await Wire.open(port);
    first.write(connectRequest("127.0.0.1", upstream.port, token));
    expect(await first.readUntil("\r\n\r\n")).toContain("200 Connection established");
    expect(decider.calls).toEqual([]);

    const second = await Wire.open(port);
    second.write(connectRequest("127.0.0.1", upstream.port, token));
    expect(await second.readToClose()).toContain("403 Forbidden");
    expect(decider.calls).toHaveLength(1);
  });

  test("revoking credentials forgets their grants and refuses their next connection", async () => {
    const upstream = await startUpstream();
    const { port, proxy, decider } = await startProxy({ allowed_domains: [] });
    proxy.allowHost(token, "127.0.0.1", "session");
    expect(proxy.evaluateHost(token, "127.0.0.1")).toBe("allow");

    proxy.revokeCredentials(token);
    proxy.registerCredentials(token);
    expect(proxy.evaluateHost(token, "127.0.0.1")).toBe("ask");

    decider.answer = async () => ({ decision: "deny" });
    const wire = await Wire.open(port);
    wire.write(connectRequest("127.0.0.1", upstream.port, token));
    expect(await wire.readToClose()).toContain("403 Forbidden");
  });

  test("a grant belongs to the credentials that earned it", async () => {
    const { proxy } = await startProxy({ allowed_domains: [] }, [token, otherToken]);
    proxy.allowHost(token, "127.0.0.1", "session");

    expect(proxy.evaluateHost(token, "127.0.0.1")).toBe("allow");
    expect(proxy.evaluateHost(otherToken, "127.0.0.1")).toBe("ask");
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
    // Unknown credentials share one bucket, so a client cannot grow the ledger by
    // guessing; the string it offered is still on the entry for diagnostics.
    expect(proxy.denials("")[0]).toMatchObject({ credentials: "not-a-run-token", reason: "no_credentials" });
    expect(proxy.denials("not-a-run-token")).toEqual([]);
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

    proxy.setPolicy({
      enabled: true,
      allowed_domains: ["other.test"],
      denied_domains: ["example.com"],
      allow_local_binding: true,
    });

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

  test("a SOCKS5 IPv6 target is canonicalized, so a short-form deny rule still matches", async () => {
    const { port, decider, proxy } = await startProxy({
      allowed_domains: ["*"],
      denied_domains: ["2001:db8::1"],
    });
    const wire = await Wire.open(port);
    await socks5Handshake(wire, token);
    expect(await wire.read(2)).toEqual([0x01, 0x00]);

    const expanded = Buffer.from([0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01]);
    wire.write(Buffer.concat([Buffer.from([0x05, 0x01, 0x00, 0x04]), expanded, portBytes(443)]));

    expect((await wire.read(10))[1]).toBe(0x02);
    expect(decider.calls).toEqual([]);
    expect(proxy.denials(token)[0]).toMatchObject({ host: "2001:db8::1", reason: "denied" });
  });

  test("a keep-alive request is rewritten to Connection: close so the next one is checked again", async () => {
    const upstream = await startUpstream();
    const { port } = await startProxy({ allowed_domains: ["127.0.0.1"] });
    const wire = await Wire.open(port);

    wire.write(
      [
        `GET http://127.0.0.1:${upstream.port}/hello HTTP/1.1`,
        `Host: 127.0.0.1:${upstream.port}`,
        `Proxy-Authorization: Basic ${basic(token)}`,
        "Connection: keep-alive",
        "",
        "",
      ].join("\r\n"),
    );

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(upstream.received[0]).toContain("Connection: close");
    expect(upstream.received[0]?.toLowerCase()).not.toContain("keep-alive");
  });
});

describe("the HTTP head is checked before anything is forwarded", () => {
  test("a Host header naming a different origin than the target is refused", async () => {
    const { port, decider } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    wire.write(
      [
        "GET http://allowed.example.com/ HTTP/1.1",
        "Host: denied.example.com",
        `Proxy-Authorization: Basic ${basic(token)}`,
        "",
        "",
      ].join("\r\n"),
    );

    expect(await wire.readToClose()).toContain("400 Bad Request");
    expect(decider.calls).toEqual([]);
  });

  test("a bare LF inside the head is refused rather than forwarded", async () => {
    const upstream = await startUpstream();
    const { port, decider } = await startProxy({ allowed_domains: ["*"] });
    const wire = await Wire.open(port);

    wire.write(
      `GET http://127.0.0.1:${upstream.port}/ HTTP/1.1\r\nHost: 127.0.0.1:${upstream.port}\r\n` +
        `Proxy-Authorization: Basic ${basic(token)}\r\n` +
        "X-Smuggled: a\nGET /admin HTTP/1.1\r\n\r\n",
    );

    expect(await wire.readToClose()).toContain("400 Bad Request");
    expect(decider.calls).toEqual([]);
    expect(upstream.received).toEqual([]);
  });

  test("an absolute-form target with no path reaches the upstream as origin form", async () => {
    const upstream = await startUpstream();
    const { port } = await startProxy({ allowed_domains: ["127.0.0.1"] });
    const wire = await Wire.open(port);

    wire.write(
      [
        `GET http://127.0.0.1:${upstream.port}?q=1 HTTP/1.1`,
        `Host: 127.0.0.1:${upstream.port}`,
        `Proxy-Authorization: Basic ${basic(token)}`,
        "",
        "",
      ].join("\r\n"),
    );

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(upstream.received[0]).toContain("GET /?q=1 HTTP/1.1");
  });

  test("headers the request's own Connection names never reach the upstream", async () => {
    const upstream = await startUpstream();
    const { port } = await startProxy({ allowed_domains: ["127.0.0.1"] });
    const wire = await Wire.open(port);

    wire.write(
      [
        `GET http://127.0.0.1:${upstream.port}/ HTTP/1.1`,
        `Host: 127.0.0.1:${upstream.port}`,
        `Proxy-Authorization: Basic ${basic(token)}`,
        "Connection: x-hop",
        "X-Hop: secret",
        "Transfer-Encoding: chunked",
        "",
        "",
      ].join("\r\n"),
    );

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(upstream.received[0]).not.toContain("X-Hop");
    expect(upstream.received[0]).not.toContain("Transfer-Encoding");
  });
});

describe("the reader reads by demand", () => {
  test("a reader nobody is asking stops reading, so bytes stay in the client's socket", async () => {
    const heads: SocketReader[] = [];
    const server = createServer((socket) => {
      cleanups.push(() => {
        socket.destroy();
      });
      const reader = new SocketReader(socket);
      heads.push(reader);
      // Read one head, then hold the connection exactly as a held decision does.
      void reader.until("\r\n\r\n", 65_536).catch(() => {});
    });
    await new Promise<void>((resolve) => server.listen({ host: "127.0.0.1", port: 0 }, () => resolve()));
    const address = server.address();
    if (address === null || typeof address === "string") throw new Error("no TCP address");
    cleanups.push(() => new Promise<void>((resolve) => server.close(() => resolve())));

    const wire = await Wire.open(address.port);
    wire.write("GET / HTTP/1.1\r\nHost: x\r\n\r\n");
    wire.write(Buffer.alloc(8 * 1024 * 1024, 0x61));
    for (let round = 0; round < 50; round++) await drainEventLoop();

    const reader = heads[0];
    expect(reader).toBeDefined();
    const { sent } = wire.progress;
    // The kernel took real traffic, and almost none of it reached this process: the
    // reader is at most one chunk past the head it was asked for.
    expect(sent).toBeGreaterThan(1_000_000);
    expect(reader?.buffered ?? 0).toBeLessThan(sent - 1_000_000);
  });

  test("a held connection still delivers every byte once the decider allows", async () => {
    const payload = Buffer.alloc(2 * 1024 * 1024, 0x62);
    let counted: (total: number) => void = () => {};
    const drained = new Promise<number>((resolve) => (counted = resolve));
    const server = createServer((socket) => {
      let total = 0;
      socket.on("data", (chunk: Buffer) => {
        total += chunk.length;
        if (total >= payload.length) counted(total);
      });
    });
    await new Promise<void>((resolve) => server.listen({ host: "127.0.0.1", port: 0 }, () => resolve()));
    const address = server.address();
    if (address === null || typeof address === "string") throw new Error("no TCP address");
    cleanups.push(() => new Promise<void>((resolve) => server.close(() => resolve())));

    const { port, decider } = await startProxy({ allowed_domains: [] });
    const wire = await Wire.open(port);
    let release: (decision: NetworkDecision) => void = () => {};
    decider.answer = () => new Promise<NetworkDecision>((resolve) => (release = resolve));
    const asked = new Promise<void>((resolve) => (decider.onCall = resolve));

    wire.write(connectRequest("127.0.0.1", address.port, token));
    await asked;
    wire.write(payload);
    release({ decision: "allow" });

    expect(await wire.readUntil("\r\n\r\n")).toContain("200 Connection established");
    expect(await drained).toBe(payload.length);
  });
});

describe("the local network guard", () => {
  const internal = { credentials: token, host: "internal.example.com", port: 443, protocol: "https_connect" } as const;

  test("a hostname resolving to a private address is denied even when it is allowlisted", async () => {
    const { port, decider, proxy } = await startProxy(
      { allowed_domains: ["internal.example.com"], allow_local_binding: false },
      [token],
      async () => ["10.0.0.5"],
    );
    const wire = await Wire.open(port);

    wire.write(connectRequest("internal.example.com", 443, token));
    const response = await wire.readToClose();

    expect(response).toContain("403 Forbidden");
    expect(decider.calls).toEqual([]);
    expect(proxy.denials(token)[0]?.reason).toBe("not_allowed_local");
  });

  test("a hostname whose lookup fails is denied, because a failed lookup proves nothing", async () => {
    const { port, decider, proxy } = await startProxy(
      { allowed_domains: ["internal.example.com"], allow_local_binding: false },
      [token],
      async () => {
        throw new Error("EAI_AGAIN");
      },
    );
    const wire = await Wire.open(port);

    wire.write(connectRequest("internal.example.com", 443, token));

    expect(await wire.readToClose()).toContain("403 Forbidden");
    expect(decider.calls).toEqual([]);
    expect(proxy.denials(token)[0]?.reason).toBe("not_allowed_local");
  });

  test("a hostname whose lookup never answers is denied once the DNS timeout expires", async () => {
    const { proxy, decider } = await startProxy(
      { allowed_domains: ["internal.example.com"], allow_local_binding: false },
      [token],
      () => new Promise<string[]>(() => {}),
    );

    jest.useFakeTimers();
    try {
      const verdict = proxy.authorize({ ...internal });
      jest.advanceTimersByTime(2_000);
      expect(await verdict).toEqual({ allowed: false, reason: "not_allowed_local" });
    } finally {
      jest.useRealTimers();
    }
    expect(decider.calls).toEqual([]);
  });

  test("a name that resolves public, then private, is denied at connect time", async () => {
    const answers = [["93.184.216.34"], ["127.0.0.1"]];
    const { port, decider, proxy } = await startProxy(
      { allowed_domains: ["rebind.example.com"], allow_local_binding: false },
      [token],
      async () => answers.shift() ?? ["127.0.0.1"],
    );
    const wire = await Wire.open(port);

    wire.write(connectRequest("rebind.example.com", 443, token));
    const response = await wire.readToClose();

    expect(response).toContain("403 Forbidden");
    expect(decider.calls).toEqual([]);
    expect(proxy.denials(token)[0]?.reason).toBe("not_allowed_local");
  });

  test("SOCKS5 answers the same connect-time rejection with reply 0x02", async () => {
    const answers = [["93.184.216.34"], ["127.0.0.1"]];
    const { port } = await startProxy(
      { allowed_domains: ["rebind.example.com"], allow_local_binding: false },
      [token],
      async () => answers.shift() ?? ["127.0.0.1"],
    );
    const wire = await Wire.open(port);

    await socks5Handshake(wire, token);
    expect(await wire.read(2)).toEqual([0x01, 0x00]);

    expect(await socks5Connect(wire, "rebind.example.com", 443)).toBe(0x02);
  });

  test("an address the resolver offers first but nothing listens on is skipped", async () => {
    const upstream = await startUpstream();
    // 127.0.0.2 is loopback here and refuses on every port; the upstream is on .1.
    const { port } = await startProxy({ allowed_domains: ["many.example.com"] }, [token], async () => [
      "127.0.0.2",
      "127.0.0.1",
    ]);
    const wire = await Wire.open(port);

    wire.write(connectRequest("many.example.com", upstream.port, token));
    expect(await wire.readUntil("\r\n\r\n")).toContain("200 Connection established");
    wire.write("ping\n");

    expect(await wire.readToClose()).toContain("UPSTREAM");
  });

  test("an exactly allowlisted localhost still reaches a loopback upstream", async () => {
    const upstream = await startUpstream();
    const policy = { allowed_domains: ["localhost"], allow_local_binding: false };
    const { port, decider } = await startProxy(policy, [token], async () => ["127.0.0.1"]);
    const wire = await Wire.open(port);

    wire.write(connectRequest("localhost", upstream.port, token));
    expect(await wire.readUntil("\r\n\r\n")).toContain("200 Connection established");
    wire.write("ping\n");

    expect(await wire.readToClose()).toContain("UPSTREAM");
    expect(decider.calls).toEqual([]);
  });
});
