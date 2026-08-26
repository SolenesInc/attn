import { describe, expect, test } from "bun:test";
import { availableModels, piAgentDir } from "../automode/models";

const ready = async () => ({ ready: true });

function files(entries: Record<string, unknown>): (path: string) => string | undefined {
  return (path: string) => {
    for (const [name, body] of Object.entries(entries)) {
      if (path.endsWith(name)) return typeof body === "string" ? body : JSON.stringify(body);
    }
    return undefined;
  };
}

describe("the models pi can reach", () => {
  test("reads the agent dir pi itself uses, and the override", () => {
    expect(piAgentDir({ PI_CODING_AGENT_DIR: "/somewhere/else" })).toBe("/somewhere/else");
    expect(piAgentDir({ PI_CODING_AGENT_DIR: "  " })).toEndWith("/.pi/agent");
    expect(piAgentDir({})).toEndWith("/.pi/agent");
  });

  test("answers each provider's models, sorted, with when the catalog was read", async () => {
    const answer = await availableModels(
      {},
      files({
        "models-store.json": {
          openai: { models: [{ id: "gpt-b" }, { id: "gpt-a", name: "GPT A" }], checkedAt: 1787510377998 },
        },
      }),
      ready,
    );
    expect(answer.problem).toBeUndefined();
    expect(answer.providers).toHaveLength(1);
    const [openai] = answer.providers;
    expect(openai.provider).toBe("openai");
    expect(openai.checkedAt).toBe(1787510377998);
    expect(openai.models.map((model) => model.id)).toEqual(["gpt-a", "gpt-b"]);
    expect(openai.models[0].name).toBe("GPT A");
  });

  test("a provider the user added joins the cached ones, and wins on the same id", async () => {
    const answer = await availableModels(
      {},
      files({
        "models-store.json": { vendor: { models: [{ id: "one", name: "cached" }] } },
        "models.json": {
          providers: {
            vendor: { models: [{ id: "one", name: "user" }, { id: "two" }] },
            other: { models: [{ id: "solo" }] },
          },
        },
      }),
      ready,
    );
    expect(answer.providers.map((provider) => provider.provider)).toEqual(["other", "vendor"]);
    const vendor = answer.providers.find((provider) => provider.provider === "vendor")!;
    expect(vendor.models.map((model) => model.id)).toEqual(["one", "two"]);
    expect(vendor.models[0].name).toBe("user");
  });

  test("readiness is what pi answers, and an unusable provider says why", async () => {
    const answer = await availableModels(
      {},
      files({ "models-store.json": { openai: { models: [{ id: "gpt" }] } } }),
      async () => ({ ready: false, detail: "no credential" }),
    );
    expect(answer.providers[0].ready).toBe(false);
    expect(answer.providers[0].detail).toBe("no credential");
  });

  test("a catalog it cannot read is a problem, not a throw", async () => {
    const answer = await availableModels({}, files({ "models-store.json": "{ not json" }), ready);
    expect(answer.providers).toEqual([]);
    expect(answer.problem).toContain("not readable JSON");
  });

  test("no catalog at all is an empty answer rather than a failure", async () => {
    const answer = await availableModels({}, () => undefined, ready);
    expect(answer.providers).toEqual([]);
    expect(answer.problem).toBeUndefined();
  });
});
