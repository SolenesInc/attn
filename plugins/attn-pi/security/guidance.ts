import { renderPrompt } from "../automode/prompt-catalog";
import type { SecurityPolicy } from "./policy";

export const securityPrompt = (event: string, values: Record<string, string> = {}): string => renderPrompt(event, values, "pi-security");
export function writeRecovery(policy: SecurityPolicy): string {
  return securityPrompt("write-recovery", { write_paths: policy.allowWrite.join(", ") });
}

export function sandboxRecovery(failure: "permission" | "network"): string {
  return securityPrompt("recovery", { network_failure: String(failure === "network") });
}

export function securityInstructions(policy: SecurityPolicy): string {
  return securityPrompt("instructions", {
    enabled: String(policy.enabled), sandbox: policy.enabled ? "enabled" : "disabled",
    network: policy.enabled ? policy.network : "unrestricted (sandbox disabled)",
    write_paths: JSON.stringify(policy.allowWrite),
    cache_paths: policy.buildCaches.enabled ? JSON.stringify(policy.cacheWritePaths) : "disabled",
    has_unavailable_caches: String(policy.unavailableCaches.length > 0), unavailable_caches: JSON.stringify(policy.unavailableCaches),
  });
}
