import type { SecurityPolicy } from "./policy";

export const reviewUnavailable = "Extra sandbox access was not granted: auto mode is off or no reviewer is configured. Continue within the sandbox, or explain the exact command, path and reason to the user. The user can enable /auto on with a configured classifier or grant the directory with /security allow-write <directory>. Do not turn security off or edit its settings yourself.";

export function writeRecovery(policy: SecurityPolicy, reviewAvailable: boolean): string {
  return `Writes are allowed in: ${policy.allowWrite.join(", ")}. ` + (reviewAvailable
    ? 'If the task needs another existing directory, retry bash with sandbox: {allowWrite: ["/existing/exact/cache-directory"], reason: "Why this command needs this directory"}. Submit the request directly; auto mode reviews it before execution. A sandbox failure is not an auto-mode refusal.'
    : reviewUnavailable);
}

export function sandboxRecovery(policy: SecurityPolicy, reviewAvailable: boolean, failure: "permission" | "network"): string {
  return [
    failure === "network"
      ? "The command reported a network error while tool networking was blocked. This may be the sandbox restriction; an ordinary network failure can look similar."
      : "The command reported a permission error while the Pi sandbox was active. The sandbox may be the cause; ordinary file permissions can also fail this way.",
    failure === "network" ? (reviewAvailable ? networkRequest : reviewUnavailable) : writeRecovery(policy, reviewAvailable),
    ...(failure === "permission" && policy.network === "deny" && reviewAvailable ? [networkRequest] : []),
    "An approved request applies only to this execution and its children. Protected paths and credential filtering remain enforced. Follow any review refusal's instructions; do not repeat an unchanged request or evade it through another tool.",
  ].join("\n");
}

const networkRequest = 'For necessary network access, retry bash with sandbox: {network: "allow", reason: "What the command needs to reach and why"}. Submit this request directly to auto mode.';

export function securityInstructions(policy: SecurityPolicy, reviewAvailable: boolean): string {
  return [
    "Pi execution permissions:",
    `Sandbox: ${policy.enabled ? "enabled" : "disabled"}. Tool network: ${policy.enabled ? policy.network : "unrestricted (sandbox disabled)"}. Credential filtering: enabled.`,
    ...(policy.enabled ? [
      `Writable paths: ${JSON.stringify(policy.allowWrite)}.`,
      `Build-cache grants: ${policy.buildCaches.enabled ? JSON.stringify(policy.cacheWritePaths) : "disabled"}. Use these caches normally; they need no extra sandbox request. The command still follows auto-mode policy.`,
      ...(policy.unavailableCaches.length ? [`Unavailable cache grants: ${JSON.stringify(policy.unavailableCaches)}.`] : []),
      `Auto-mode access review: ${reviewAvailable ? "available" : "unavailable"}.`,
      reviewAvailable
        ? 'A sandbox execution failure is not an auto-mode refusal. For a necessary command needing additional access, retry bash with sandbox.allowWrite (specific existing directories) and/or sandbox.network ("allow"), plus sandbox.reason explaining the command, target and task need. Submit the request directly without first asking the user in chat. Approval covers one execution and its children, without changing saved settings.'
        : reviewUnavailable,
      "Use a project-local cache when it is a straightforward equivalent. Do not change the intended operation, skip tests, or change security settings to avoid review. Permission and DNS/download errors can have ordinary causes; check whether the requested access would address the failure.",
      "After a review refusal, follow its stated recovery instructions. Use a materially safer alternative or explain the exact command, access, refusal and needed user decision. Retry after relevant user approval only when the refusal permits it. Do not disguise the action or repeat an unchanged request without new information. Continue independent work.",
    ] : ["Omit bash.sandbox while the sandbox is disabled; normal auto-mode review still applies when enabled."]),
    "Pi removes sensitive environment variables from bash and filters credentials from tool results. Sandbox requests cannot restore credentials or override protected paths. Explain such requirements to the user.",
  ].join("\n");
}
