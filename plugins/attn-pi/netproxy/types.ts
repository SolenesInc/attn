export type NetworkPolicy = { enabled: boolean; allowed_domains: string[]; denied_domains: string[] };
export type NetworkProtocol = "http" | "https_connect" | "socks5_tcp";
export type HostDecision = "allow" | "deny";
export type NetworkRequest = { credentials: string; host: string; port: number; protocol: NetworkProtocol };
/** `scope: "session"` tells the proxy to remember the grant for these credentials. */
export type NetworkDecision = { decision: HostDecision; scope?: "once" | "session" };
export type Decider = (request: NetworkRequest) => Promise<NetworkDecision>;
export type DenialReason = "denied" | "not_allowed" | "not_allowed_local" | "no_credentials" | "decider_unavailable";
export type ProxyDenial = {
  credentials: string;
  host: string;
  port: number;
  protocol: NetworkProtocol;
  at: string;
  reason: DenialReason;
};

export type GateVerdict = { allowed: true } | { allowed: false; reason: DenialReason };

/** What the HTTP and SOCKS5 listeners need from the proxy to serve one connection. */
export type ProxyGate = {
  knowsCredentials(credentials: string): boolean;
  authorize(request: NetworkRequest): Promise<GateVerdict>;
  recordDenial(request: NetworkRequest, reason: DenialReason): void;
};

export function denialBody(host: string): string {
  return `Network access to "${host}" is blocked by policy.`;
}

// Codex's x-proxy-error taxonomy (responses.rs:51-60); the client sees why the
// allowlist refused without parsing prose.
export function denialHeaderValue(reason: DenialReason): string {
  if (reason === "denied") return "blocked-by-denylist";
  if (reason === "not_allowed" || reason === "not_allowed_local") return "blocked-by-allowlist";
  return "blocked-by-policy";
}
