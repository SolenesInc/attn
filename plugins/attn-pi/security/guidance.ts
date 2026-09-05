import { renderPrompt } from "../automode/prompt-catalog";
import type { SecurityPolicy } from "./policy";

export const securityPrompt = (event: string, values: Record<string, string> = {}): string => renderPrompt(event, values, "pi-security");
export const reviewUnavailable = securityPrompt("review-unavailable");

export function writeRecovery(policy: SecurityPolicy, reviewAvailable: boolean): string {
  return securityPrompt("write-recovery", { write_paths: policy.allowWrite.join(", "), review_available: String(reviewAvailable) });
}

export function sandboxRecovery(policy: SecurityPolicy, reviewAvailable: boolean, failure: "permission" | "network"): string {
  return securityPrompt("recovery", {
    write_paths: policy.allowWrite.join(", "), review_available: String(reviewAvailable),
    network_failure: String(failure === "network"), network_denied: String(policy.network === "deny"),
  });
}

export function securityInstructions(policy: SecurityPolicy, reviewAvailable: boolean): string {
  return securityPrompt("instructions", {
    enabled: String(policy.enabled), sandbox: policy.enabled ? "enabled" : "disabled",
    network: policy.enabled ? policy.network : "unrestricted (sandbox disabled)",
    write_paths: JSON.stringify(policy.allowWrite),
    cache_paths: policy.buildCaches.enabled ? JSON.stringify(policy.cacheWritePaths) : "disabled",
    has_unavailable_caches: String(policy.unavailableCaches.length > 0), unavailable_caches: JSON.stringify(policy.unavailableCaches),
    review: reviewAvailable ? "available" : "unavailable", review_available: String(reviewAvailable),
  });
}
