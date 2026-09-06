// Host normalization, wildcard domain rules and local/private address classification,
// ported from codex-rs/network-proxy/src/policy.rs.

/** Trim, unwrap brackets, drop a single `:port`, lowercase, drop trailing dots. */
export function normalizeHost(input: string): string {
  const host = input.trim();
  if (host.startsWith("[")) {
    const end = host.indexOf("]");
    if (end >= 0) return normalizeDnsHostOrIpLiteral(host.slice(1, end));
  }
  // The proxy stack hands us a bare host, but be defensive: strip `:port` only when
  // there is exactly one colon, so unbracketed IPv6 literals survive (policy.rs:105-118).
  if (countColons(host) === 1) return normalizeDnsHostOrIpLiteral(host.split(":")[0] ?? "");
  return normalizeDnsHostOrIpLiteral(host);
}

function countColons(value: string): number {
  let count = 0;
  for (const character of value) if (character === ":") count += 1;
  return count;
}

function normalizeDnsHostOrIpLiteral(input: string): string {
  const lowered = input.toLowerCase();
  const trimmed = trimEnd(lowered, ".");
  return normalizeIpLiteral(trimmed) ?? trimmed;
}

function trimEnd(value: string, character: string): string {
  let end = value.length;
  while (end > 0 && value[end - 1] === character) end -= 1;
  return value.slice(0, end);
}

function normalizeIpLiteral(host: string): string | undefined {
  if (parseIp(host) !== undefined) return host;
  for (const delimiter of ["%25", "%"]) {
    const at = host.indexOf(delimiter);
    if (at < 0) continue;
    const ip = host.slice(0, at);
    const scope = host.slice(at + delimiter.length);
    if (parseIp(ip) !== undefined) return `${ip}%${scope}`;
  }
  return undefined;
}

/** The address part of a scoped IPv6 literal (`fe80::1%lo0`), when there is one. */
export function unscopedIpLiteral(host: string): string | undefined {
  const at = host.indexOf("%");
  if (at < 0) return undefined;
  const ip = host.slice(0, at);
  return parseIp(ip) === undefined ? undefined : ip;
}

export function isLoopbackHost(normalized: string): boolean {
  const host = unscopedIpLiteral(normalized) ?? normalized;
  if (host === "localhost") return true;
  const ip = parseIp(host);
  return ip !== undefined && isLoopbackParsedIp(ip);
}

/** True when the host is an IP literal (or `localhost`) that names a non-public address. */
export function isLocalLiteral(normalized: string): boolean {
  if (isLoopbackHost(normalized)) return true;
  const host = unscopedIpLiteral(normalized) ?? normalized;
  const ip = parseIp(host);
  return ip !== undefined && isNonPublicParsedIp(ip);
}

export function isNonPublicIp(value: string): boolean {
  const ip = parseIp(value);
  return ip !== undefined && isNonPublicParsedIp(ip);
}

type ParsedIp = { kind: "v4"; value: number } | { kind: "v6"; groups: number[] };

export function parseIp(value: string): ParsedIp | undefined {
  const v4 = parseIpv4(value);
  if (v4 !== undefined) return { kind: "v4", value: v4 };
  const v6 = parseIpv6(value);
  return v6 === undefined ? undefined : { kind: "v6", groups: v6 };
}

function parseIpv4(value: string): number | undefined {
  const parts = value.split(".");
  if (parts.length !== 4) return undefined;
  let result = 0;
  for (const part of parts) {
    if (!/^(0|[1-9]\d{0,2})$/.test(part)) return undefined;
    const octet = Number(part);
    if (octet > 255) return undefined;
    result = result * 256 + octet;
  }
  return result;
}

function parseIpv6(value: string): number[] | undefined {
  if (!value.includes(":")) return undefined;
  const halves = value.split("::");
  if (halves.length > 2) return undefined;
  const head = halves[0] === "" ? [] : (halves[0] ?? "").split(":");
  const tail = halves.length === 2 ? (halves[1] === "" ? [] : (halves[1] ?? "").split(":")) : undefined;
  const groups = (segments: string[]): number[] | undefined => {
    const out: number[] = [];
    for (const [index, segment] of segments.entries()) {
      const embedded = index === segments.length - 1 ? parseIpv4(segment) : undefined;
      if (embedded !== undefined) {
        out.push(Math.floor(embedded / 0x10000), embedded % 0x10000);
        continue;
      }
      if (!/^[0-9a-f]{1,4}$/.test(segment)) return undefined;
      out.push(Number.parseInt(segment, 16));
    }
    return out;
  };
  const left = groups(head);
  if (left === undefined) return undefined;
  if (tail === undefined) return left.length === 8 ? left : undefined;
  const right = groups(tail);
  if (right === undefined) return undefined;
  if (left.length + right.length > 7) return undefined;
  return [...left, ...Array<number>(8 - left.length - right.length).fill(0), ...right];
}

function isLoopbackParsedIp(ip: ParsedIp): boolean {
  if (ip.kind === "v4") return ipv4InCidr(ip.value, [127, 0, 0, 0], 8);
  const embedded = ipv6ToIpv4(ip.groups);
  if (embedded !== undefined && ipv4InCidr(embedded, [127, 0, 0, 0], 8)) return true;
  return ip.groups.slice(0, 7).every((group) => group === 0) && ip.groups[7] === 1;
}

function isNonPublicParsedIp(ip: ParsedIp): boolean {
  if (ip.kind === "v4") return isNonPublicIpv4(ip.value);
  return isNonPublicIpv6(ip.groups);
}

function isNonPublicIpv4(ip: number): boolean {
  return (
    ipv4InCidr(ip, [127, 0, 0, 0], 8) || // loopback
    ipv4InCidr(ip, [10, 0, 0, 0], 8) || // private
    ipv4InCidr(ip, [172, 16, 0, 0], 12) ||
    ipv4InCidr(ip, [192, 168, 0, 0], 16) ||
    ipv4InCidr(ip, [169, 254, 0, 0], 16) || // link local
    ipv4InCidr(ip, [224, 0, 0, 0], 4) || // multicast
    ip === 0xff_ff_ff_ff || // broadcast
    ipv4InCidr(ip, [0, 0, 0, 0], 8) || // "this network" (RFC 1122)
    ipv4InCidr(ip, [100, 64, 0, 0], 10) || // CGNAT (RFC 6598)
    ipv4InCidr(ip, [192, 0, 0, 0], 24) || // IETF protocol assignments (RFC 6890)
    ipv4InCidr(ip, [192, 0, 2, 0], 24) || // TEST-NET-1 (RFC 5737)
    ipv4InCidr(ip, [198, 18, 0, 0], 15) || // benchmarking (RFC 2544)
    ipv4InCidr(ip, [198, 51, 100, 0], 24) || // TEST-NET-2 (RFC 5737)
    ipv4InCidr(ip, [203, 0, 113, 0], 24) || // TEST-NET-3 (RFC 5737)
    ipv4InCidr(ip, [240, 0, 0, 0], 4) // reserved (RFC 6890)
  );
}

function ipv4InCidr(ip: number, base: number[], prefix: number): boolean {
  const baseValue = ((base[0] ?? 0) * 256 + (base[1] ?? 0)) * 65536 + (base[2] ?? 0) * 256 + (base[3] ?? 0);
  const size = 2 ** (32 - prefix);
  const floor = Math.floor(baseValue / size) * size;
  return ip >= floor && ip < floor + size;
}

function ipv6ToIpv4(groups: number[]): number | undefined {
  const leadingZero = groups.slice(0, 5).every((group) => group === 0);
  if (!leadingZero) return undefined;
  const marker = groups[5];
  if (marker !== 0 && marker !== 0xff_ff) return undefined;
  return (groups[6] ?? 0) * 65536 + (groups[7] ?? 0);
}

function isNonPublicIpv6(groups: number[]): boolean {
  const embedded = ipv6ToIpv4(groups);
  if (embedded !== undefined) {
    return isNonPublicIpv4(embedded) || (groups.slice(0, 7).every((group) => group === 0) && groups[7] === 1);
  }
  const first = groups[0] ?? 0;
  const loopback = groups.slice(0, 7).every((group) => group === 0) && groups[7] === 1;
  const unspecified = groups.every((group) => group === 0);
  const multicast = (first & 0xff_00) === 0xff_00;
  const uniqueLocal = (first & 0xfe_00) === 0xfc_00;
  const linkLocal = (first & 0xff_c0) === 0xfe_80;
  return loopback || unspecified || multicast || uniqueLocal || linkLocal;
}

function normalizePattern(pattern: string): string {
  const trimmed = pattern.trim();
  if (trimmed === "*") return "*";
  const prefix = trimmed.startsWith("**.") ? "**." : trimmed.startsWith("*.") ? "*." : "";
  const remainder = normalizeHost(trimmed.slice(prefix.length));
  return prefix === "" ? remainder : `${prefix}${remainder}`;
}

// Supported patterns: `example.com` exact, `*.example.com` subdomains only,
// `**.example.com` apex and subdomains, `*` everything (policy.rs:213-219).
function expandDomainPattern(pattern: string): string[] {
  if (pattern === "*") return ["*"];
  if (pattern.startsWith("**.")) {
    const domain = pattern.slice(3).trim();
    if (domain === "") return [""];
    return [domain, `?*.${domain}`];
  }
  if (pattern.startsWith("*.")) {
    const domain = pattern.slice(2).trim();
    if (domain === "") return [""];
    return [`?*.${domain}`];
  }
  return [pattern];
}

function globToRegExp(glob: string): RegExp {
  let source = "^";
  for (const character of glob) {
    if (character === "*") source += ".*";
    else if (character === "?") source += ".";
    else source += character.replaceAll(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }
  return new RegExp(`${source}$`, "i");
}

/** Compiled allow or deny domain rules; `matches` takes an already normalized host. */
export class DomainMatcher {
  private readonly patterns: RegExp[];

  constructor(patterns: string[]) {
    const seen = new Set<string>();
    this.patterns = [];
    for (const pattern of patterns) {
      for (const candidate of expandDomainPattern(normalizePattern(pattern))) {
        if (candidate === "" || seen.has(candidate)) continue;
        seen.add(candidate);
        this.patterns.push(globToRegExp(candidate));
      }
    }
  }

  matches(normalizedHost: string): boolean {
    const unscoped = unscopedIpLiteral(normalizedHost);
    return this.patterns.some(
      (pattern) => pattern.test(normalizedHost) || (unscoped !== undefined && pattern.test(unscoped)),
    );
  }
}

// Local/loopback targets are reachable only through an exact allowlist entry; a wildcard
// never opens the local network (runtime.rs:1058-1073).
export function isExplicitLocalAllowlisted(allowedDomains: string[], normalizedHost: string): boolean {
  const unscoped = unscopedIpLiteral(normalizedHost);
  return allowedDomains.some((raw) => {
    const pattern = raw.trim();
    if (pattern === "*" || pattern.startsWith("*.") || pattern.startsWith("**.")) return false;
    if (pattern.includes("*") || pattern.includes("?")) return false;
    const normalized = normalizeHost(pattern);
    return normalized === normalizedHost || (unscoped !== undefined && normalized === unscoped);
  });
}
