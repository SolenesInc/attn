// codex-rs/sandboxing/src/denial.rs:13-88. Nothing here is decisive on its own:
// a keyword match wins over the exit code, and both are heuristics.
const keywords = [
  "operation not permitted", "permission denied", "read-only file system",
  "seccomp", "sandbox", "landlock", "failed to write file",
];

const quickRejectExitCodes = [2, 126, 127];
const linuxSigsys = 31;

export type SandboxRunResult = { sandboxed: boolean; exitCode: number | null; signal?: string | null; output: string };

export function isSandboxDenial(result: SandboxRunResult): boolean {
  if (!result.sandboxed || result.exitCode === 0) return false;
  const lower = result.output.toLowerCase();
  if (keywords.some((keyword) => lower.includes(keyword))) return true;
  if (result.exitCode !== null && quickRejectExitCodes.includes(result.exitCode)) return false;
  if (result.signal === "SIGSYS") return true;
  return process.platform === "linux" && result.exitCode === 128 + linuxSigsys;
}
