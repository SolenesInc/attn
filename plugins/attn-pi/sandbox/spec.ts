import { statSync } from "node:fs";
import { canonical, within } from "../security/policy";

export type SandboxMode = "read-only" | "workspace-write" | "danger-full-access";
export type SandboxPermissions = "use_default" | "require_escalated";
/** Each shell execution gets its own proxy credentials; the relay token never reaches the sandbox. */
export type ProxyAddress = { host: "127.0.0.1"; port: number; credentials: string };

/** "proxy" asks for attn's proxy; sandboxSpecFor falls back to "off" when none is running. */
export type NetworkAccess = "off" | "proxy" | "unrestricted";
export type NetworkMode = { mode: "off" } | { mode: "proxy"; proxy: ProxyAddress } | { mode: "unrestricted" };

export type SandboxSpec = {
  mode: SandboxMode;
  cwd: string;
  writableRoots: string[];
  denyRead: string[];
  denyWrite: string[];
  temp: string;
  network: NetworkMode;
};

export type SandboxConfig = {
  mode: SandboxMode;
  network: NetworkAccess;
  allowWrite: string[];
  denyRead: string[];
  denyWrite: string[];
  cacheWritePaths: string[];
};

export function outermostRoots(paths: string[]): string[] {
  const ordered = [...new Set(paths.map(canonical))].sort((left, right) => left.length - right.length);
  return ordered.filter((path, index) => !ordered.slice(0, index).some((parent) => within(path, parent)));
}

// protocol.rs:1275-1286 grants /tmp on unix when it is a directory; /dev/shm is
// how a workspace gets POSIX semaphores (exec/tests/suite/sandbox.rs:166-171).
export function platformWritableRoots(): string[] {
  if (process.platform === "win32") return [];
  return ["/tmp", "/dev/shm"].filter((path) => {
    try { return statSync(path).isDirectory(); } catch { return false; }
  });
}

export function sandboxSpecFor(
  config: SandboxConfig,
  cwd: string,
  temp: string,
  opts: { permissions: SandboxPermissions; proxy?: ProxyAddress },
): SandboxSpec | "unsandboxed" {
  if (config.mode === "danger-full-access") return "unsandboxed";
  // Codex refuses this while denied reads exist (core/src/tools/sandboxing.rs:275-283).
  // attn asks a reviewer first, and that approval covers escaping the deny list too.
  if (opts.permissions === "require_escalated") return "unsandboxed";
  const resolvedCwd = canonical(cwd);
  const resolvedTemp = canonical(temp);
  // Every configured root stays its own root, nested or not: each one carries an
  // anchor deny, and collapsing a nested root would drop that boundary.
  const writableRoots = config.mode === "workspace-write"
    ? [...new Set([resolvedCwd, resolvedTemp, ...platformWritableRoots(), ...config.allowWrite, ...config.cacheWritePaths].map(canonical))]
    : [];
  // A "proxy" policy with no running proxy is attn's own network policy it
  // cannot enforce here, so the command runs offline rather than unrestricted.
  const network: NetworkMode = config.network === "unrestricted" ? { mode: "unrestricted" }
    : config.network === "proxy" && opts.proxy ? { mode: "proxy", proxy: opts.proxy }
    : { mode: "off" };
  return {
    mode: config.mode,
    cwd: resolvedCwd,
    temp: resolvedTemp,
    writableRoots,
    denyRead: outermostRoots(config.denyRead),
    denyWrite: [...new Set(config.denyWrite.map(canonical))],
    network,
  };
}

export function proxyUrl(proxy: ProxyAddress, scheme: "http" | "socks5h"): string {
  return `${scheme}://${encodeURIComponent(proxy.credentials)}@${proxy.host}:${proxy.port}`;
}
