import { commandEnvironment } from "../sandbox/environment";
import { sandboxArgv, shellQuote, type SandboxedCommand } from "../sandbox/exec";
import { sandboxSpecFor, type SandboxSpec } from "../sandbox/spec";
import type { SecurityPolicy } from "./policy";

export type { SandboxedCommand };
export { shellQuote };

/** The pre-Codex settings file has one sandboxed profile; disabling it is danger-full-access. */
export function specForPolicy(policy: SecurityPolicy): SandboxSpec | "unsandboxed" {
  return sandboxSpecFor({
    mode: policy.enabled ? "workspace-write" : "danger-full-access",
    network: policy.network === "allow",
    allowWrite: policy.allowWrite,
    denyRead: policy.denyRead,
    denyWrite: policy.denyWrite,
    cacheWritePaths: policy.cacheWritePaths,
  }, policy.cwd, policy.temp, { permissions: "use_default" });
}

/** The single place any sandboxed child's environment is built. */
export function sandboxEnvironment(policy: SecurityPolicy, env: NodeJS.ProcessEnv): Record<string, string> {
  return commandEnvironment(specForPolicy(policy), env);
}

export function sandboxCommand(policy: SecurityPolicy, executable: string, args: string[], platform = process.platform): SandboxedCommand {
  return sandboxArgv(specForPolicy(policy), executable, args, platform);
}

export function bashSandbox(policy: SecurityPolicy, command: string): string {
  const invocation = sandboxCommand(policy, "/bin/bash", ["--noprofile", "--norc", "-c", command]);
  return [invocation.executable, ...invocation.args].map(shellQuote).join(" ");
}
