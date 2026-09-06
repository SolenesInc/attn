import { emptyEnvironment, readEnvironment, type Environment } from "../automode/environment";

// RawApprovalConfig IS the JSON internal/automode.Config marshals: a field renamed on one
// side and not the other silently drops to the default below.

export type Decision = "allow" | "prompt" | "forbidden";

export type ApprovalPolicy = "untrusted" | "on-request" | "never";

export type SandboxMode = "read-only" | "workspace-write" | "danger-full-access";

export const decisions: readonly Decision[] = ["allow", "prompt", "forbidden"];

export const approvalPolicies: readonly ApprovalPolicy[] = ["untrusted", "on-request", "never"];

export const sandboxModes: readonly SandboxMode[] = [
  "read-only",
  "workspace-write",
  "danger-full-access",
];

// A pattern token is one command word, or the alternatives that word may take. The
// examples are pi's to validate; the daemon only carries them.
export type Rule = {
  pattern: readonly (readonly string[])[];
  decision: Decision;
  justification: string;
  match: readonly (readonly string[])[];
  notMatch: readonly (readonly string[])[];
};

export type Network = {
  enabled: boolean;
  allowedDomains: readonly string[];
  deniedDomains: readonly string[];
  // Off unless the daemon says otherwise: the proxy hard denies a host that
  // resolves to a loopback or private address while this is false.
  allowLocalBinding: boolean;
};

export type ApprovalConfig = {
  enabledDefault: boolean;
  approvalPolicy: ApprovalPolicy;
  sandboxMode: SandboxMode;
  rules: readonly Rule[];
  network: Network;
  environment: Environment;
  legacyPatterns: readonly string[];
};

export type RawApprovalConfig = {
  enabled_default?: unknown;
  approval_policy?: unknown;
  sandbox_mode?: unknown;
  rules?: unknown;
  network?: unknown;
  environment?: unknown;
  legacy_patterns?: unknown;
};

export class ApprovalConfigError extends Error {
  constructor(
    readonly field: string,
    message: string,
  ) {
    super(message);
    this.name = "ApprovalConfigError";
  }
}

export const defaultNetwork: Network = {
  enabled: true,
  allowedDomains: [],
  deniedDomains: [],
  allowLocalBinding: false,
};

export const defaultApprovalConfig: ApprovalConfig = {
  enabledDefault: true,
  approvalPolicy: "on-request",
  sandboxMode: "workspace-write",
  rules: [],
  network: defaultNetwork,
  environment: emptyEnvironment,
  legacyPatterns: [],
};

export function loadApprovalConfig(raw: RawApprovalConfig | undefined): ApprovalConfig {
  if (raw === undefined || raw === null) return defaultApprovalConfig;
  return {
    enabledDefault: readBoolean(
      raw.enabled_default,
      "enabled_default",
      defaultApprovalConfig.enabledDefault,
    ),
    approvalPolicy: readChoice(
      raw.approval_policy,
      "approval_policy",
      approvalPolicies,
      defaultApprovalConfig.approvalPolicy,
    ),
    sandboxMode: readChoice(
      raw.sandbox_mode,
      "sandbox_mode",
      sandboxModes,
      defaultApprovalConfig.sandboxMode,
    ),
    rules: readRules(raw.rules),
    network: readNetwork(raw.network),
    environment: readEnvironmentOrFail(raw.environment),
    legacyPatterns: readStrings(raw.legacy_patterns, "legacy_patterns").map((entry) => entry.trim()),
  };
}

export function ruleDescription(rule: Rule): string {
  return rule.pattern
    .map((alternatives) => (alternatives.length === 1 ? alternatives[0] : `{${alternatives.join("|")}}`))
    .join(" ");
}

function readRules(value: unknown): readonly Rule[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new ApprovalConfigError("rules", "rules must be a list");
  return value.map((entry, index) => readRule(entry, `rules[${index}]`));
}

function readRule(value: unknown, field: string): Rule {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ApprovalConfigError(field, `${field} must be an object`);
  }
  const raw = value as {
    pattern?: unknown;
    decision?: unknown;
    justification?: unknown;
    match?: unknown;
    not_match?: unknown;
  };
  const pattern = readPattern(raw.pattern, `${field}.pattern`);
  const decision = readChoice(raw.decision, `${field}.decision`, decisions, "allow");
  const justification = readString(raw.justification, `${field}.justification`);
  if (decision === "forbidden" && justification === "") {
    throw new ApprovalConfigError(
      `${field}.justification`,
      `forbidden rule ${JSON.stringify(pattern.map((token) => token.join("|")).join(" "))} has no ` +
        `justification: it is the text the session is given when the command is refused`,
    );
  }
  return {
    pattern,
    decision,
    justification,
    match: readExamples(raw.match, `${field}.match`),
    notMatch: readExamples(raw.not_match, `${field}.not_match`),
  };
}

function readPattern(value: unknown, field: string): readonly (readonly string[])[] {
  if (!Array.isArray(value) || value.length === 0) {
    throw new ApprovalConfigError(field, `${field} must be a non-empty list of command tokens`);
  }
  return value.map((token, index) => readToken(token, `${field}[${index}]`));
}

function readToken(value: unknown, field: string): readonly string[] {
  const alternatives = typeof value === "string" ? [value] : readStrings(value, field);
  if (alternatives.length === 0) throw new ApprovalConfigError(field, `${field} has no alternatives`);
  return alternatives.map((alternative, index) => {
    const trimmed = alternative.trim();
    if (trimmed === "") throw new ApprovalConfigError(field, `${field}[${index}] is blank`);
    if (/\s/.test(trimmed)) {
      throw new ApprovalConfigError(
        field,
        `${field}[${index}] (${JSON.stringify(alternative)}) holds whitespace: a prefix rule ` +
          `takes one command token per entry, not a shell line`,
      );
    }
    return trimmed;
  });
}

function readExamples(value: unknown, field: string): readonly (readonly string[])[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new ApprovalConfigError(field, `${field} must be a list of commands`);
  return value.map((entry, index) => readStrings(entry, `${field}[${index}]`));
}

function readNetwork(value: unknown): Network {
  if (value === undefined || value === null) return defaultNetwork;
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new ApprovalConfigError("network", "network must be an object");
  }
  const raw = value as {
    enabled?: unknown;
    allowed_domains?: unknown;
    denied_domains?: unknown;
    allow_local_binding?: unknown;
  };
  return {
    enabled: readBoolean(raw.enabled, "network.enabled", defaultNetwork.enabled),
    allowedDomains: readDomains(raw.allowed_domains, "network.allowed_domains"),
    deniedDomains: readDomains(raw.denied_domains, "network.denied_domains"),
    allowLocalBinding: readBoolean(
      raw.allow_local_binding,
      "network.allow_local_binding",
      defaultNetwork.allowLocalBinding,
    ),
  };
}

function readDomains(value: unknown, field: string): readonly string[] {
  return readStrings(value, field)
    .map((domain) => domain.trim())
    .filter((domain) => domain !== "");
}

function readEnvironmentOrFail(raw: unknown): Environment {
  try {
    return readEnvironment(raw);
  } catch (error) {
    throw new ApprovalConfigError("environment", (error as Error).message);
  }
}

function readStrings(value: unknown, field: string): string[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new ApprovalConfigError(field, `${field} must be a list of strings`);
  return value.map((entry, index) => {
    if (typeof entry !== "string") throw new ApprovalConfigError(field, `${field}[${index}] must be a string`);
    return entry;
  });
}

function readChoice<T extends string>(
  value: unknown,
  field: string,
  choices: readonly T[],
  fallback: T,
): T {
  if (value === undefined || value === null) return fallback;
  if (typeof value !== "string") throw new ApprovalConfigError(field, `${field} must be a string`);
  const trimmed = value.trim();
  if (trimmed === "") return fallback;
  if (!choices.includes(trimmed as T)) {
    throw new ApprovalConfigError(field, `unknown ${field} ${JSON.stringify(trimmed)} (want ${choices.join(", ")})`);
  }
  return trimmed as T;
}

function readString(value: unknown, field: string): string {
  if (value === undefined || value === null) return "";
  if (typeof value !== "string") throw new ApprovalConfigError(field, `${field} must be a string`);
  return value.trim();
}

function readBoolean(value: unknown, field: string, fallback: boolean): boolean {
  if (value === undefined || value === null) return fallback;
  if (typeof value !== "boolean") throw new ApprovalConfigError(field, `${field} must be a boolean`);
  return value;
}
