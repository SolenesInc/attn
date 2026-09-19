import { renderPrompt } from "../automode/prompt-catalog";
import type { SandboxMode } from "../sandbox/spec";
import type { SecurityPolicy } from "./policy";

export const securityPrompt = (event: string, values: Record<string, string> = {}): string => renderPrompt(event, values, "pi-security");
export function writeRecovery(policy: SecurityPolicy): string {
  return securityPrompt("write-recovery", { write_paths: policy.allowWrite.join(", ") });
}

export function sandboxRecovery(failure: "permission" | "network"): string {
  return securityPrompt("recovery", { network_failure: String(failure === "network") });
}

// A proxied Linux sandbox closes its network namespace entirely (see bwrap.ts);
// the proxy and its host allowlist only take effect on macOS until seed s-00asxp.
const linuxNetworkNotice = "unavailable inside the Linux sandbox; a command that needs the network runs outside the sandbox after review (sandbox_permissions=require_escalated)";
// attn asked for network access but its proxy is not running (sandboxSpecFor
// fails closed), so the sandbox has none on either platform until it is.
const missingProxyNotice = "unavailable inside the sandbox: attn configured network policy but its proxy is not running; a command that needs the network runs outside the sandbox after review (sandbox_permissions=require_escalated)";

export function securityInstructions(
  policy: SecurityPolicy,
  { platform = process.platform, attnNetwork, sandboxMode = "workspace-write" }:
    { platform?: string; attnNetwork?: "proxy" | "missing"; sandboxMode?: SandboxMode } = {},
): string {
  return securityPrompt("instructions", {
    file_tools_read_only: String(sandboxMode === "read-only"),
    file_tools_full_access: String(sandboxMode === "danger-full-access"),
    enabled: String(policy.enabled), sandbox: policy.enabled ? "enabled" : "disabled",
    network: !policy.enabled ? "unrestricted (sandbox disabled)"
      : attnNetwork === "missing" ? missingProxyNotice
      : attnNetwork === "proxy" && platform === "linux" ? linuxNetworkNotice
      : policy.network,
    write_paths: JSON.stringify(sandboxMode === "read-only" ? [] : policy.allowWrite),
    cache_paths: policy.buildCaches.enabled ? JSON.stringify(policy.cacheWritePaths) : "disabled",
    has_unavailable_caches: String(policy.unavailableCaches.length > 0), unavailable_caches: JSON.stringify(policy.unavailableCaches),
  });
}
