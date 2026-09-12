import { getSupportedThinkingLevels, type Api, type Model } from "@earendil-works/pi-ai/compat";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";

export type GuardianSelection = { provider?: string; model?: string; effort?: string };
export const guardianEfforts = ["off", "minimal", "low", "medium", "high", "xhigh", "max"] as const;
export type GuardianEffort = typeof guardianEfforts[number];
export type GuardianSettings = {
  selection: GuardianSelection;
  source: "attn default" | "session override";
  effective: string;
  problem?: string;
  models: { provider: string; id: string }[];
  efforts: string[];
};
export type GuardianControl = {
  snapshot(ctx: ExtensionContext): GuardianSettings;
  change(command: string, ctx: ExtensionContext): Promise<void>;
};

export function readGuardianSelection(value: unknown): GuardianSelection {
  if (value === undefined) return {};
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("guardian must be an object");
  const raw = value as Record<string, unknown>;
  const result: GuardianSelection = {};
  for (const field of ["provider", "model", "effort"] as const) {
    const value = raw[field];
    if (value === undefined || value === "") continue;
    if (typeof value !== "string" || /\s/.test(value)) throw new Error(`guardian ${field} must be a string without whitespace`);
    result[field] = value;
  }
  if (Boolean(result.provider) !== Boolean(result.model)) throw new Error("guardian provider and model must both be set, or both empty to follow the session model");
  if (result.effort && !guardianEfforts.includes(result.effort as GuardianEffort)) throw new Error(`unknown guardian effort ${JSON.stringify(result.effort)}; choose default or ${guardianEfforts.join(", ")}`);
  return result;
}

export function resolveGuardian(selection: GuardianSelection, ctx: ExtensionContext): { model: Model<Api>; effort: GuardianEffort | undefined } {
  const model = selection.provider && selection.model ? ctx.modelRegistry.find(selection.provider, selection.model) : ctx.model;
  if (!model) throw new Error(`Guardian model ${selection.provider ? `${selection.provider}/${selection.model}` : "(session model)"} is unavailable. Choose a model in /security or use /auto off.`);
  const supported = getSupportedThinkingLevels(model);
  if (selection.effort && !supported.includes(selection.effort as GuardianEffort)) throw new Error(`Guardian ${model.provider}/${model.id} does not support effort ${selection.effort}; supported: ${supported.join(", ")}. Choose an effort in /security.`);
  const effort = selection.effort as GuardianEffort | undefined ?? (supported.includes("low") ? "low" : undefined);
  return { model, effort };
}

export function guardianSettings(selection: GuardianSelection, overridden: boolean, ctx: ExtensionContext): GuardianSettings {
  const snapshot: GuardianSettings = { selection: { ...selection }, source: overridden ? "session override" : "attn default", effective: "unavailable", models: ctx.modelRegistry.getAvailable().map(({ provider, id }) => ({ provider, id })), efforts: [] };
  try {
    const { model, effort } = resolveGuardian(selection, ctx);
    snapshot.effective = `${model.provider}/${model.id} · effort ${effort ?? "provider default"}`;
    snapshot.efforts = getSupportedThinkingLevels(model);
  } catch (error) { snapshot.problem = error instanceof Error ? error.message : String(error); }
  return snapshot;
}
