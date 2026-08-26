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
    unset: "the repository the session started in and its configured remotes",
  },
  {
    id: "repo_visibility",
    label: "Repository visibility",
    kind: "choice",
    choices: ["private", "public"],
    unset: "assume private unless the transcript shows otherwise",
  },
  { id: "domains", label: "Trusted internal domains", kind: "list", unset: "None configured" },
  { id: "buckets", label: "Trusted cloud buckets", kind: "list", unset: "None configured" },
  { id: "services", label: "Key internal services", kind: "list", unset: "None configured" },
  {
    id: "source_control",
    label: "Source-control orgs",
    kind: "list",
    unset: "the trusted repo and its remotes only",
  },
  { id: "registry", label: "Internal package registry", kind: "list", unset: "None configured" },
  {
    id: "sensitive_data",
    label: "Sensitive data locations",
    kind: "list",
    unset: "any store holding personal, confidential, credential or regulated material",
  },
  {
    id: "audiences",
    label: "Cleared audiences",
    kind: "list",
    unset: "None configured, so nobody is cleared",
  },
  {
    id: "remote_targets",
    label: "Sensitive remote targets",
    kind: "list",
    unset: "any name carrying prod or production as a whole word",
  },
  {
    id: "iac_scopes",
    label: "Protected IaC scopes",
    kind: "list",
    unset: "IAM, RBAC, networking, quota and node pools, and anything carrying prod",
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
    throw new Error(`environment must be an object with slots and notes, got ${typeof raw}`);
  }
  const source = raw as { slots?: unknown; notes?: unknown };
  const slots: Record<string, string[]> = {};
  if (source.slots !== undefined && source.slots !== null) {
    if (typeof source.slots !== "object" || Array.isArray(source.slots)) {
      throw new Error("environment.slots must be an object of slot id to entries");
    }
    for (const [id, values] of Object.entries(source.slots as Record<string, unknown>)) {
      if (!Array.isArray(values)) throw new Error(`environment slot ${id} must be a list`);
      const entries = values.filter((v): v is string => typeof v === "string" && v.trim() !== "");
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
    lines.push(`- **${slot.label}**: ${value}`);
  }
  const notes = env.notes.filter((line) => line.trim() !== "");
  if (notes.length > 0) {
    lines.push("");
    lines.push("The user also said this about the machine. It is context, not a list:");
    for (const line of notes) lines.push(`> ${line}`);
  }
  return lines.join("\n");
}
