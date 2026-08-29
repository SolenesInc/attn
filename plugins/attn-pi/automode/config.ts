import { emptyEnvironment, readEnvironment, type Environment } from "./environment";

export type AutoModeConfig = {
  enabledDefault: boolean;

  environment: Environment;

  allow: readonly string[];

  hardDeny: readonly string[];

  models: readonly string[];
};

export type RawAutoModeConfig = {
  enabled_default?: unknown;
  environment?: unknown;
  allow?: unknown;
  hard_deny?: unknown;

  models?: unknown;

  classifier_model?: unknown;
  escalation_model?: unknown;
  classifier_models?: unknown;
  escalation_models?: unknown;
};

export class AutoModeConfigError extends Error {
  constructor(
    readonly field: string,
    message: string,
  ) {
    super(message);
    this.name = "AutoModeConfigError";
  }
}

export const defaultAutoModeConfig: AutoModeConfig = {
  enabledDefault: true,
  environment: emptyEnvironment,
  allow: [],
  hardDeny: [],
  models: [],
};

export function loadAutoModeConfig(raw: RawAutoModeConfig | undefined): AutoModeConfig {
  if (raw === undefined) return defaultAutoModeConfig;
  const allow = readPatterns(raw.allow, "allow");
  for (const pattern of allow) {
    if (isBroadPattern(pattern)) {
      throw new AutoModeConfigError(
        "allow",
        `broad allow pattern ${JSON.stringify(pattern)} is refused: an allow entry must name ` +
          `something. A blanket allow is what the classifier exists to replace.`,
      );
    }
  }
  return {
    enabledDefault: readBoolean(raw.enabled_default, "enabled_default", defaultAutoModeConfig.enabledDefault),
    environment: readEnvironmentOrFail(raw.environment),
    allow,
    hardDeny: readPatterns(raw.hard_deny, "hard_deny"),
    models: readModels(raw),
  };
}

function readEnvironmentOrFail(raw: unknown): Environment {
  try {
    return readEnvironment(raw);
  } catch (error) {
    throw new AutoModeConfigError("environment", (error as Error).message);
  }
}

export function isBroadPattern(pattern: string): boolean {
  return pattern.replace(/[*?\s]/g, "") === "";
}

export function matchesPattern(pattern: string, signature: string): boolean {
  return globToRegExp(pattern).test(signature);
}

export function matchesAnyPattern(patterns: readonly string[], signature: string): string | undefined {
  for (const pattern of patterns) if (matchesPattern(pattern, signature)) return pattern;
  return undefined;
}

function globToRegExp(pattern: string): RegExp {
  let source = "";
  for (const character of pattern) {
    if (character === "*") source += ".*";
    else if (character === "?") source += ".";
    else source += character.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }
  return new RegExp(`^${source}$`, "s");
}

function readPatterns(value: unknown, field: string): readonly string[] {
  return readStrings(value, field).map((pattern) => pattern.trim());
}

function readStrings(value: unknown, field: string): string[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new AutoModeConfigError(field, `${field} must be a list of strings`);
  return value.map((entry, index) => {
    if (typeof entry !== "string") throw new AutoModeConfigError(field, `${field}[${index}] must be a string`);
    return entry;
  });
}

function readModels(raw: RawAutoModeConfig): readonly string[] {
  if (raw.models !== undefined && raw.models !== null) return readModelList(raw.models, "models");
  const folded: string[] = [];
  for (const layer of [
    { list: raw.classifier_models, listField: "classifier_models", single: raw.classifier_model, singleField: "classifier_model" },
    { list: raw.escalation_models, listField: "escalation_models", single: raw.escalation_model, singleField: "escalation_model" },
  ]) {
    const models =
      layer.list === undefined || layer.list === null
        ? readLegacySingle(layer.single, layer.singleField)
        : readModelList(layer.list, layer.listField);
    for (const model of models) if (!folded.includes(model)) folded.push(model);
  }
  return folded;
}

function readLegacySingle(value: unknown, field: string): readonly string[] {
  const model = readString(value, field, "");
  return model === "" ? [] : [model];
}

function readModelList(value: unknown, field: string): readonly string[] {
  const models = readStrings(value, field).map((model, index) => {
    const trimmed = model.trim();
    if (trimmed === "") throw new AutoModeConfigError(field, `${field}[${index}] is blank`);
    return trimmed;
  });
  return models;
}

function readString(value: unknown, field: string, fallback: string): string {
  if (value === undefined || value === null) return fallback;
  if (typeof value !== "string") throw new AutoModeConfigError(field, `${field} must be a string`);
  const trimmed = value.trim();
  return trimmed === "" ? fallback : trimmed;
}

function readBoolean(value: unknown, field: string, fallback: boolean): boolean {
  if (value === undefined || value === null) return fallback;
  if (typeof value !== "boolean") throw new AutoModeConfigError(field, `${field} must be a boolean`);
  return value;
}
