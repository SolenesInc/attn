import { bwrapArgs, bwrapExecutable } from "./bwrap";
import { seatbeltArgs, seatbeltExecutable } from "./seatbelt";
import type { SandboxSpec } from "./spec";

export type SandboxedCommand = { executable: string; args: string[] };

export function shellQuote(value: string): string {
  return "'" + value.split("'").join("'\"'\"'") + "'";
}

export function sandboxArgv(
  spec: SandboxSpec | "unsandboxed",
  executable: string,
  args: string[],
  platform = process.platform,
): SandboxedCommand {
  if (spec === "unsandboxed") return { executable, args };
  switch (platform) {
    case "darwin": return { executable: seatbeltExecutable, args: seatbeltArgs(spec, [executable, ...args]) };
    case "linux": return { executable: bwrapExecutable(spec), args: bwrapArgs(spec, [executable, ...args]) };
    default: throw new Error(`Pi security sandbox is unavailable on ${platform}`);
  }
}

export function wrapCommand(spec: SandboxSpec, command: string): string {
  const invocation = sandboxArgv(spec, "/bin/bash", ["--noprofile", "--norc", "-c", command]);
  return [invocation.executable, ...invocation.args].map(shellQuote).join(" ");
}
