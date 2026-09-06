import { proxyUrl, type SandboxSpec } from "./spec";

// The command must never reach the approval channel it is being judged on.
const withheld = ["ATTN_PI_TOKEN", "ATTN_PI_SUITE_SOCKET"];

const httpProxyKeys = ["HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"];
const socksProxyKeys = ["ALL_PROXY", "all_proxy"];
const noProxyKeys = ["NO_PROXY", "no_proxy"];
// NO_PROXY holds a host list rather than a URL, so it is only ever overwritten.
const proxyUrlKeys = new Set([...httpProxyKeys, ...socksProxyKeys]);

// seatbelt.rs:65-67. A loopback proxy in the inherited environment is a proxy
// this machine hosts; only this run decides whether the command gets one.
function isLoopbackProxy(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed) return true;
  try {
    const host = new URL(trimmed.includes("://") ? trimmed : `http://${trimmed}`).hostname;
    return ["127.0.0.1", "localhost", "[::1]", "::1"].includes(host.toLowerCase());
  } catch { return false; }
}

export function commandEnvironment(spec: SandboxSpec | "unsandboxed", env: NodeJS.ProcessEnv): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(env)) {
    if (value === undefined || withheld.includes(key)) continue;
    if (proxyUrlKeys.has(key) && isLoopbackProxy(value)) continue;
    result[key] = value;
  }
  // An escalated command runs outside the proxy, so it must not carry its env
  // either (core/src/tools/sandboxing.rs:296-305).
  if (spec === "unsandboxed") return result;
  for (const key of ["TMPDIR", "TMP", "TEMP"]) result[key] = spec.temp;
  const proxy = spec.network.proxy;
  if (!proxy) return result;
  for (const key of httpProxyKeys) result[key] = proxyUrl(proxy, "http");
  // socks5h keeps DNS at the proxy, so the policy sees the hostname the client
  // asked for instead of an already-resolved literal.
  for (const key of socksProxyKeys) result[key] = proxyUrl(proxy, "socks5h");
  // Empty so local targets route through the proxy too (network-proxy/src/proxy.rs:760-765).
  for (const key of noProxyKeys) result[key] = "";
  return result;
}
