import { renderPrompt } from "./prompt-catalog";

// The environment schema, mirroring internal/automode/environment.go. Both
// sides pin the same ordered ids, so one cannot move without the other failing.

export type SlotKind = "list" | "choice";

export type EnvironmentSlot = {
  id: string;
  label: string;
  kind: SlotKind;
  choices?: readonly string[];
  unset: string;
};

export const environmentSlots: readonly EnvironmentSlot[] = [
  {
    id: "trusted_repo",
    label: "Trusted repo",
    kind: "list",
    unset: renderPrompt("unset-trusted_repo", {}, "pi-environment"),
  },
  {
    id: "repo_visibility",
    label: "Repository visibility",
    kind: "choice",
    choices: ["private", "public"],
    unset: renderPrompt("unset-repo_visibility", {}, "pi-environment"),
  },
  {
    id: "domains",
    label: "Trusted internal domains",
    kind: "list",
    unset: renderPrompt("unset-domains", {}, "pi-environment"),
  },
  {
    id: "buckets",
    label: "Trusted cloud buckets",
    kind: "list",
    unset: renderPrompt("unset-buckets", {}, "pi-environment"),
  },
  {
    id: "services",
    label: "Key internal services",
    kind: "list",
    unset: renderPrompt("unset-services", {}, "pi-environment"),
  },
  {
    id: "source_control",
    label: "Source-control orgs",
    kind: "list",
    unset: renderPrompt("unset-source_control", {}, "pi-environment"),
  },
  {
    id: "registry",
    label: "Internal package registry",
    kind: "list",
    unset: renderPrompt("unset-registry", {}, "pi-environment"),
  },
  {
    id: "sensitive_data",
    label: "Sensitive data locations",
    kind: "list",
    unset: renderPrompt("unset-sensitive_data", {}, "pi-environment"),
  },
  {
    id: "audiences",
    label: "Cleared audiences",
    kind: "list",
    unset: renderPrompt("unset-audiences", {}, "pi-environment"),
  },
  {
    id: "remote_targets",
    label: "Sensitive remote targets",
    kind: "list",
    unset: renderPrompt("unset-remote_targets", {}, "pi-environment"),
  },
  {
    id: "iac_scopes",
    label: "Protected IaC scopes",
    kind: "list",
    unset: renderPrompt("unset-iac_scopes", {}, "pi-environment"),
  },
];

export type Environment = {
  slots: Readonly<Record<string, readonly string[]>>;
  notes: readonly string[];
};

export const emptyEnvironment: Environment = { slots: {}, notes: [] };

export function readEnvironment(raw: unknown): Environment {
  if (raw === undefined || raw === null) return emptyEnvironment;
  if (typeof raw !== "object" || Array.isArray(raw)) {
    throw new Error(
      `environment must be an object with slots and notes, got ${typeof raw}`,
    );
  }
  const source = raw as { slots?: unknown; notes?: unknown };
  const slots: Record<string, string[]> = {};
  if (source.slots !== undefined && source.slots !== null) {
    if (typeof source.slots !== "object" || Array.isArray(source.slots)) {
      throw new Error(
        "environment.slots must be an object of slot id to entries",
      );
    }
    for (const [id, values] of Object.entries(
      source.slots as Record<string, unknown>,
    )) {
      if (!Array.isArray(values))
        throw new Error(`environment slot ${id} must be a list`);
      const entries = values.filter(
        (v): v is string => typeof v === "string" && v.trim() !== "",
      );
      if (entries.length > 0) slots[id] = entries.map((v) => v.trim());
    }
  }
  const notes = Array.isArray(source.notes)
    ? source.notes.filter((v): v is string => typeof v === "string")
    : [];
  return { slots, notes };
}

export function renderEnvironment(env: Environment): string {
  const lines: string[] = [];
  for (const slot of environmentSlots) {
    const values = env.slots[slot.id] ?? [];
    const value = values.length > 0 ? values.join(", ") : slot.unset;
    lines.push(
      renderPrompt("slot", { label: slot.label, value }, "pi-environment"),
    );
  }
  const notes = env.notes.filter((line) => line.trim() !== "");
  return renderPrompt(
    "render",
    {
      slots: lines.join("\n"),
      has_notes: String(notes.length > 0),
      notes: notes.map((line) => `> ${line}`).join("\n"),
    },
    "pi-environment",
  );
}
