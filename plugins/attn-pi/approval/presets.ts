import type { ApprovalPolicy, SandboxMode } from "./config";

export type PresetID = "read-only" | "default" | "full-access" | "untrusted";

export interface Preset {
  id: PresetID;
  label: string;
  description: string;
  approvalPolicy: ApprovalPolicy;
  sandboxMode: SandboxMode;
}

// The twin of Presets() in internal/automode/automode.go: the same four rows,
// ids, labels and descriptions. Change one table and you change both.
export const presets: readonly Preset[] = [
  {
    id: "read-only",
    label: "Read Only",
    description: "The agent can read files in the current workspace. Approval is required to edit files or access the internet.",
    approvalPolicy: "on-request",
    sandboxMode: "read-only",
  },
  {
    id: "default",
    label: "Default",
    description: "The agent can read and edit files in the current workspace, and run commands. Approval is required to access the internet or edit other files.",
    approvalPolicy: "on-request",
    sandboxMode: "workspace-write",
  },
  {
    id: "full-access",
    label: "Full Access",
    description: "The agent can edit files outside this workspace and access the internet without asking for approval. Exercise caution when using.",
    approvalPolicy: "never",
    sandboxMode: "danger-full-access",
  },
  {
    id: "untrusted",
    label: "Untrusted",
    description: "Every command that no rule allows is reviewed before it runs, and a command the sandbox refuses is reviewed before it reruns unsandboxed.",
    approvalPolicy: "untrusted",
    sandboxMode: "workspace-write",
  },
];

export function presetByID(id: string): Preset | undefined {
  return presets.find((preset) => preset.id === id);
}

export function presetFor(approvalPolicy: ApprovalPolicy, sandboxMode: SandboxMode): Preset | undefined {
  return presets.find((preset) => preset.approvalPolicy === approvalPolicy && preset.sandboxMode === sandboxMode);
}

/** A preset id when the pair is one, and the raw pair when it is not. */
export function describePermissions(approvalPolicy: ApprovalPolicy, sandboxMode: SandboxMode): string {
  return presetFor(approvalPolicy, sandboxMode)?.id ?? `${approvalPolicy}/${sandboxMode}`;
}
