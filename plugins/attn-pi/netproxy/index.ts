import { lookup as dnsLookup } from "node:dns/promises";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createServer, type Server, type Socket } from "node:net";
import { join } from "node:path";
import { serveHttp } from "./http";
import {
  DomainMatcher,
  isExplicitLocalAllowlisted,
  isLocalLiteral,
  isNonPublicIp,
  normalizeHost,
  parseIp,
  targetMatchesNonPublicAddress,
  unscopedIpLiteral,
} from "./policy";
import { serveSocks5 } from "./socks5";
import { ConnectionClosedError, SocketReader, connectAny } from "./stream";
import type {
  Decider,
  DenialReason,
  DialResult,
  GateVerdict,
  HostDecision,
  NetworkDecision,
  NetworkPolicy,
  NetworkRequest,
  ProxyDenial,
  ProxyGate,
} from "./types";

export type {
  Decider,
  DenialReason,
  DialResult,
  HostDecision,
  NetworkDecision,
  NetworkPolicy,
  NetworkProtocol,
  NetworkRequest,
  ProxyDenial,
} from "./types";

const socksVersion = 0x05;

// Codex's DNS_LOOKUP_TIMEOUT (runtime.rs:50); the receipt is Codex's. A lookup that
// does not answer inside it denies the host, exactly as a lookup error does.
const dnsLookupTimeoutMs = 2_000;

// Codex keeps the last 200 blocked requests per proxy (runtime.rs:49); here the ledger
// is per credentials so one busy session cannot evict another session's denials.
const maxDenialsPerCredentials = 200;

/** One proxy per profile, shared by every pi session; each session gets its own proxy
 * credentials, so every connection is attributed to the session that made it. */
export class NetworkProxy implements ProxyGate {
  private policy: NetworkPolicy;
  private allowMatcher: DomainMatcher;
  private denyMatcher: DomainMatcher;
  private readonly stateDir: string;
  private readonly decide: Decider;
  private readonly lookup: (host: string) => Promise<string[]>;
  private server: Server | undefined;
  private readonly sockets = new Set<Socket>();
  private readonly credentials = new Set<string>();
  private readonly sessionGrants = new Map<string, Set<string>>();
  private readonly onceGrants = new Map<string, Map<string, number>>();
  private readonly denialLedger = new Map<string, ProxyDenial[]>();

  constructor(options: {
    policy: NetworkPolicy;
    stateDir: string;
    decide: Decider;
    lookup?: (host: string) => Promise<string[]>;
  }) {
    this.policy = normalizePolicy(options.policy);
    this.allowMatcher = new DomainMatcher(this.policy.allowed_domains);
    this.denyMatcher = new DomainMatcher(this.policy.denied_domains);
    this.stateDir = options.stateDir;
    this.decide = options.decide;
    this.lookup = options.lookup ?? systemLookup;
  }

  async listen(): Promise<{ host: "127.0.0.1"; port: number }> {
    if (this.server) throw new Error("the network proxy is already listening");
    const server = createServer((socket) => {
      this.sockets.add(socket);
      socket.on("close", () => this.sockets.delete(socket));
      void this.serve(socket);
    });
    const remembered = this.rememberedPort();
    const port = await bind(server, remembered).catch((error: unknown) => {
      if (remembered === 0) throw error;
      return bind(server, 0);
    });
    this.server = server;
    this.rememberPort(port);
    return { host: "127.0.0.1", port };
  }

  async close(): Promise<void> {
    const server = this.server;
    this.server = undefined;
    for (const socket of this.sockets) socket.destroy();
    this.sockets.clear();
    if (!server) return;
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }

  setPolicy(policy: NetworkPolicy): void {
    this.policy = normalizePolicy(policy);
    this.allowMatcher = new DomainMatcher(this.policy.allowed_domains);
    this.denyMatcher = new DomainMatcher(this.policy.denied_domains);
  }

  registerCredentials(credentials: string): void {
    if (credentials !== "") this.credentials.add(credentials);
  }

  // Also drops the credentials' denial ledger: this proxy outlives every session in the
  // profile, so keeping per-session history would grow for as long as the driver runs.
  revokeCredentials(credentials: string): void {
    this.credentials.delete(credentials);
    this.sessionGrants.delete(credentials);
    this.onceGrants.delete(credentials);
    this.denialLedger.delete(credentials);
  }

  allowHost(credentials: string, host: string, scope: "once" | "session"): void {
    const normalized = normalizeHost(host);
    if (scope === "session") {
      const grants = this.sessionGrants.get(credentials) ?? new Set<string>();
      grants.add(normalized);
      this.sessionGrants.set(credentials, grants);
      return;
    }
    const grants = this.onceGrants.get(credentials) ?? new Map<string, number>();
    grants.set(normalized, (grants.get(normalized) ?? 0) + 1);
    this.onceGrants.set(credentials, grants);
  }

  /** Denials for these credentials, oldest first; `since` returns only what came after it. */
  denials(credentials: string, since?: string): ProxyDenial[] {
    const entries = this.denialLedger.get(credentials) ?? [];
    return since === undefined ? [...entries] : entries.filter((entry) => entry.at > since);
  }

  /** The literal policy answer alone: no decider, no grant consumed, no I/O. The
   * resolved-address check that can still deny an "allow" runs in `authorize`. */
  evaluateHost(credentials: string, host: string): HostDecision | "ask" {
    const normalized = normalizeHost(host);
    if (this.literalDenyReason(normalized) !== undefined) return "deny";
    if (this.allowMatcher.matches(normalized)) return "allow";
    return this.hasGrant(credentials, normalized) ? "allow" : "ask";
  }

  knowsCredentials(credentials: string): boolean {
    return this.credentials.has(credentials);
  }

  async authorize(request: NetworkRequest): Promise<GateVerdict> {
    const normalized = normalizeHost(request.host);
    const hardDeny = await this.hardDenyReason(normalized);
    if (hardDeny !== undefined) {
      this.recordDenial(request, hardDeny);
      return { allowed: false, reason: hardDeny };
    }
    if (this.allowMatcher.matches(normalized) || this.takeGrant(request.credentials, normalized)) {
      return { allowed: true };
    }

    let answer: NetworkDecision;
    try {
      answer = await this.decide({ ...request, host: normalized });
    } catch {
      this.recordDenial(request, "decider_unavailable");
      return { allowed: false, reason: "decider_unavailable" };
    }
    if (answer?.decision !== "allow") {
      this.recordDenial(request, "not_allowed");
      return { allowed: false, reason: "not_allowed" };
    }
    if (answer.scope === "session") this.allowHost(request.credentials, normalized, "session");
    return { allowed: true };
  }

  // Keyed by credentials this proxy issued, never by what the client sent: otherwise a
  // client mints a new ledger bucket per guess and the map grows without limit.
  recordDenial(request: NetworkRequest, reason: DenialReason): void {
    const key = this.credentials.has(request.credentials) ? request.credentials : "";
    const entries = this.denialLedger.get(key) ?? [];
    entries.push({
      credentials: request.credentials,
      host: request.host,
      port: request.port,
      protocol: request.protocol,
      at: new Date().toISOString(),
      reason,
    });
    if (entries.length > maxDenialsPerCredentials) entries.splice(0, entries.length - maxDenialsPerCredentials);
    this.denialLedger.set(key, entries);
  }

  /** Connects to the vetted host, re-checking the addresses it actually resolves to.
   * Codex re-checks at connect time because a name can resolve differently twice. */
  async dial(request: NetworkRequest): Promise<DialResult> {
    const normalized = normalizeHost(request.host);
    let addresses: string[];
    try {
      addresses = await this.resolve(normalized);
    } catch (error) {
      return { outcome: "unreachable", error };
    }
    const targets = addresses.filter((address) => this.allowsTarget(normalized, address));
    if (targets.length === 0) {
      this.recordDenial(request, "not_allowed_local");
      return { outcome: "denied", reason: "not_allowed_local" };
    }
    try {
      return { outcome: "connected", socket: await connectAny(targets, request.port) };
    } catch (error) {
      return { outcome: "unreachable", error };
    }
  }

  // Decision order is Codex's (runtime.rs:578-632): an explicit deny always wins, then
  // local/private targets, which only an exact allowlist entry opens.
  private literalDenyReason(normalizedHost: string): DenialReason | undefined {
    if (!this.policy.enabled) return "denied";
    if (this.denyMatcher.matches(normalizedHost)) return "denied";
    if (this.policy.allow_local_binding) return undefined;
    if (isLocalLiteral(normalizedHost) && !isExplicitLocalAllowlisted(this.policy.allowed_domains, normalizedHost)) {
      return "not_allowed_local";
    }
    return undefined;
  }

  // A local literal was already opened (or refused) by the exact allowlist above; a name
  // is resolved, and one resolving off-public is denied even when allowlisted (runtime.rs:612-625).
  private async hardDenyReason(normalizedHost: string): Promise<DenialReason | undefined> {
    const literal = this.literalDenyReason(normalizedHost);
    if (literal !== undefined || this.policy.allow_local_binding) return literal;
    if (isLocalLiteral(normalizedHost)) return undefined;
    return (await this.resolvesToNonPublic(normalizedHost)) ? "not_allowed_local" : undefined;
  }

  // "Block the request if this DNS lookup fails. We resolve the hostname again when we
  // connect, so a failed check here does not prove the destination is public." (runtime.rs:978-980)
  private async resolvesToNonPublic(normalizedHost: string): Promise<boolean> {
    try {
      const addresses = await this.resolve(normalizedHost);
      return addresses.some((address) => isNonPublicIp(address) || parseIp(address) === undefined);
    } catch {
      return true;
    }
  }

  private async resolve(normalizedHost: string): Promise<string[]> {
    const literal = unscopedIpLiteral(normalizedHost) ?? normalizedHost;
    if (parseIp(literal) !== undefined) return [literal];
    return withTimeout(this.lookup(normalizedHost), dnsLookupTimeoutMs, normalizedHost);
  }

  private allowsTarget(normalizedHost: string, address: string): boolean {
    if (!isNonPublicIp(address)) return parseIp(address) !== undefined;
    if (this.policy.allow_local_binding) return true;
    return targetMatchesNonPublicAddress(normalizedHost, address) && this.literalDenyReason(normalizedHost) === undefined;
  }

  private hasGrant(credentials: string, normalizedHost: string): boolean {
    if (this.sessionGrants.get(credentials)?.has(normalizedHost)) return true;
    return (this.onceGrants.get(credentials)?.get(normalizedHost) ?? 0) > 0;
  }

  private takeGrant(credentials: string, normalizedHost: string): boolean {
    if (this.sessionGrants.get(credentials)?.has(normalizedHost)) return true;
    const grants = this.onceGrants.get(credentials);
    const remaining = grants?.get(normalizedHost) ?? 0;
    if (remaining <= 0) return false;
    if (remaining === 1) grants?.delete(normalizedHost);
    else grants?.set(normalizedHost, remaining - 1);
    return true;
  }

  private async serve(socket: Socket): Promise<void> {
    socket.on("error", () => socket.destroy());
    const reader = new SocketReader(socket);
    try {
      const first = await reader.peek(1);
      if (first[0] === socksVersion) await serveSocks5(socket, reader, this);
      else await serveHttp(socket, reader, this);
    } catch (error) {
      if (!(error instanceof ConnectionClosedError)) {
        console.error(`attn-pi: network proxy dropped a connection: ${String(error)}`);
      }
      socket.destroy();
    }
  }

  private get portFile(): string {
    return join(this.stateDir, "proxy.json");
  }

  private rememberedPort(): number {
    try {
      const parsed: unknown = JSON.parse(readFileSync(this.portFile, "utf8"));
      const port = (parsed as { port?: unknown } | null)?.port;
      return typeof port === "number" && Number.isInteger(port) && port > 0 && port <= 65_535 ? port : 0;
    } catch {
      return 0;
    }
  }

  private rememberPort(port: number): void {
    try {
      mkdirSync(this.stateDir, { recursive: true, mode: 0o700 });
      writeFileSync(this.portFile, `${JSON.stringify({ port })}\n`, { mode: 0o600 });
    } catch (error) {
      console.error(`attn-pi: could not persist the network proxy port to ${this.portFile}: ${String(error)}`);
    }
  }
}

function normalizePolicy(policy: NetworkPolicy): NetworkPolicy {
  return {
    enabled: policy.enabled === true,
    allowed_domains: [...(policy.allowed_domains ?? [])],
    denied_domains: [...(policy.denied_domains ?? [])],
    allow_local_binding: policy.allow_local_binding === true,
  };
}

async function systemLookup(host: string): Promise<string[]> {
  const answers = await dnsLookup(host, { all: true });
  return answers.map((answer) => answer.address);
}

function withTimeout<T>(work: Promise<T>, ms: number, host: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`DNS lookup for ${host} exceeded ${ms}ms`)), ms);
    work.then(resolve, reject).finally(() => clearTimeout(timer));
  });
}

function bind(server: Server, port: number): Promise<number> {
  return new Promise((resolve, reject) => {
    const onError = (error: Error): void => reject(error);
    server.once("error", onError);
    server.listen({ host: "127.0.0.1", port }, () => {
      server.off("error", onError);
      const address = server.address();
      if (address === null || typeof address === "string") {
        reject(new Error("the network proxy listener has no TCP address"));
        return;
      }
      resolve(address.port);
    });
  });
}

/** Reads the `network` block of an auto-mode config; no block at all means undefined,
 * a block saying `enabled: false` is a policy that denies every host. */
export function networkPolicyFrom(autoMode: unknown): NetworkPolicy | undefined {
  const network = (autoMode as { network?: unknown } | null | undefined)?.network;
  if (typeof network !== "object" || network === null || Array.isArray(network)) return undefined;
  const record = network as {
    enabled?: unknown;
    allowed_domains?: unknown;
    denied_domains?: unknown;
    allow_local_binding?: unknown;
  };
  return {
    enabled: record.enabled === true,
    allowed_domains: stringList(record.allowed_domains),
    denied_domains: stringList(record.denied_domains),
    allow_local_binding: record.allow_local_binding === true,
  };
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : [];
}
