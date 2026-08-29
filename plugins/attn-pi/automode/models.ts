import { homedir } from "node:os";
import { join } from "node:path";

export type EnvironmentLike = Record<string, string | undefined>;

export const modelStoreFileName = "models-store.json";
export const userCatalogFileName = "models.json";

export type CatalogModel = {
  id: string;
  name?: string;
  contextWindow?: number;
};

export type ProviderModels = {
  provider: string;
  ready: boolean;
  detail?: string;
  checkedAt?: number;
  models: CatalogModel[];
};

export type AvailableModels = {
  providers: ProviderModels[];
  problem?: string;
};

export type AuthCheck = (provider: string) => Promise<{ ready: boolean; detail?: string }>;

export function piAgentDir(env: EnvironmentLike): string {
  const configured = env.PI_CODING_AGENT_DIR?.trim();
  return configured && configured !== "" ? configured : join(homedir(), ".pi", "agent");
}

export async function availableModels(
  env: EnvironmentLike,
  readFile: (path: string) => string | undefined,
  checkAuth: AuthCheck,
): Promise<AvailableModels> {
  const dir = piAgentDir(env);
  const problems: string[] = [];
  const catalog = new Map<string, { models: CatalogModel[]; checkedAt?: number }>();

  const store = readJSON(join(dir, modelStoreFileName), readFile, problems);
  if (isRecord(store)) {
    for (const [provider, entry] of Object.entries(store)) {
      if (!isRecord(entry)) continue;
      catalog.set(provider, {
        models: readModels(entry.models),
        checkedAt: typeof entry.checkedAt === "number" ? entry.checkedAt : undefined,
      });
    }
  }

  const user = readJSON(join(dir, userCatalogFileName), readFile, problems);
  if (isRecord(user) && isRecord(user.providers)) {
    for (const [provider, entry] of Object.entries(user.providers)) {
      if (!isRecord(entry)) continue;
      const existing = catalog.get(provider);
      const models = mergeModels(existing?.models ?? [], readModels(entry.models));
      catalog.set(provider, { models, checkedAt: existing?.checkedAt });
    }
  }

  const entries = [...catalog];
  const auth = await Promise.all(entries.map(([provider]) => checkAuth(provider)));
  const providers: ProviderModels[] = entries.map(([provider, entry], index) => ({
    provider,
    ready: auth[index].ready,
    detail: auth[index].detail,
    checkedAt: entry.checkedAt,
    models: entry.models,
  }));
  providers.sort((left, right) => left.provider.localeCompare(right.provider));

  return { providers, problem: problems.length > 0 ? problems.join("; ") : undefined };
}

function readModels(value: unknown): CatalogModel[] {
  if (!Array.isArray(value)) return [];
  const models: CatalogModel[] = [];
  for (const entry of value) {
    if (!isRecord(entry)) continue;
    const id = typeof entry.id === "string" ? entry.id.trim() : "";
    if (id === "") continue;
    models.push({
      id,
      name: typeof entry.name === "string" && entry.name.trim() !== "" ? entry.name : undefined,
      contextWindow: typeof entry.contextWindow === "number" ? entry.contextWindow : undefined,
    });
  }
  models.sort((left, right) => left.id.localeCompare(right.id));
  return models;
}

function mergeModels(base: CatalogModel[], extra: CatalogModel[]): CatalogModel[] {
  const byID = new Map(base.map((model) => [model.id, model]));
  for (const model of extra) byID.set(model.id, model);
  return [...byID.values()].sort((left, right) => left.id.localeCompare(right.id));
}

function readJSON(
  path: string,
  readFile: (path: string) => string | undefined,
  problems: string[],
): unknown {
  let contents: string | undefined;
  try {
    contents = readFile(path);
  } catch (error) {
    problems.push(`${path} could not be read: ${message(error)}`);
    return undefined;
  }
  if (contents === undefined || contents.trim() === "") return undefined;
  try {
    return JSON.parse(contents);
  } catch (error) {
    problems.push(`${path} is not readable JSON: ${message(error)}`);
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
