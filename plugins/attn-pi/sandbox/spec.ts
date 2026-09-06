import { canonical, within } from "../security/policy";

export type SandboxMode = "read-only" | "workspace-write" | "danger-full-access";
export type SandboxPermissions = "use_default" | "require_escalated";
export type ProxyAddress = { host: "127.0.0.1"; port: number; credentials: string };

export type SandboxSpec = {
  mode: SandboxMode;
  cwd: string;
  writableRoots: string[];
  denyRead: string[];
  denyWrite: string[];
  temp: string;
  network: { enabled: boolean; proxy?: ProxyAddress };
};

export type SandboxConfig = {
  mode: SandboxMode;
  network: boolean;
  allowWrite: string[];
  denyRead: string[];
  denyWrite: string[];
  cacheWritePaths: string[];
};

/** Drops duplicates and paths already covered by a shorter root. */
export function outermostRoots(paths: string[]): string[] {
  const ordered = [...new Set(paths.map(canonical))].sort((left, right) => left.length - right.length);
  return ordered.filter((path, index) => !ordered.slice(0, index).some((parent) => within(path, parent)));
}

export function sandboxSpecFor(
  config: SandboxConfig,
  cwd: string,
  temp: string,
  opts: { permissions: SandboxPermissions; proxy?: ProxyAddress },
): SandboxSpec | "unsandboxed" {
  // config_toml.rs:809 maps danger-full-access to PermissionProfile::Disabled.
  if (config.mode === "danger-full-access") return "unsandboxed";
  // Codex keeps an escalated command sandboxed when denied reads exist
  // (core/src/tools/sandboxing.rs:275-283); attn escalates only after approval.
  if (opts.permissions === "require_escalated") return "unsandboxed";
  const resolvedCwd = canonical(cwd);
  const resolvedTemp = canonical(temp);
  const writableRoots = config.mode === "workspace-write"
    ? outermostRoots([resolvedCwd, resolvedTemp, ...config.allowWrite, ...config.cacheWritePaths])
    : [];
  const proxy = config.network ? opts.proxy : undefined;
  return {
    mode: config.mode,
    cwd: resolvedCwd,
    temp: resolvedTemp,
    writableRoots,
    denyRead: outermostRoots(config.denyRead),
    denyWrite: [...new Set(config.denyWrite.map(canonical))],
    network: { enabled: config.network, ...(proxy ? { proxy } : {}) },
  };
}

export function proxyUrl(proxy: ProxyAddress, scheme: "http" | "socks5h"): string {
  return `${scheme}://${encodeURIComponent(proxy.credentials)}@${proxy.host}:${proxy.port}`;
}
