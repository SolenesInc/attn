const ghPRCreatePattern = /\bgh\s+pr\s+create\b/;

const pullRequestURLPattern =
  /https:\/\/[A-Za-z0-9][A-Za-z0-9.-]*\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/pull\/[0-9]+/g;

/** The URLs a `gh pr create` printed, in order, each once. */
export function pullRequestsCreated(toolName: string, toolInput: unknown, toolOutput: string): string[] {
  if (!isShellTool(toolName)) return [];
  if (!ghPRCreatePattern.test(shellCommand(toolInput))) return [];
  return [...new Set(toolOutput.match(pullRequestURLPattern) ?? [])];
}

function isShellTool(name: string): boolean {
  const cleaned = name.trim().toLowerCase();
  return cleaned === "bash" || cleaned === "shell" || cleaned === "exec_command" || cleaned.endsWith(".exec_command");
}

function shellCommand(toolInput: unknown): string {
  if (typeof toolInput !== "object" || toolInput === null || Array.isArray(toolInput)) return "";
  const record = toolInput as Record<string, unknown>;
  for (const key of ["command", "cmd"]) {
    const value = record[key];
    if (typeof value === "string") return value;
    // Codex's exec form is an argv list: `["bash", "-lc", "gh pr create …"]`.
    if (Array.isArray(value) && value.every((entry) => typeof entry === "string")) return value.join(" ");
  }
  return "";
}
