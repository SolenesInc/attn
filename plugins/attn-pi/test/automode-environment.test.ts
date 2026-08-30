import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { environmentSlots, readEnvironment, renderEnvironment } from "../automode/environment";

describe("the environment schema", () => {
  // internal/automode/environment.go is this schema's other half. The ids and
  // their order must agree, or a filled slot renders as unset on the other side.
  test("mirrors the ids and the order Go ships", () => {
    const source = readFileSync(new URL("../../../internal/automode/environment.go", import.meta.url), "utf8");
    const goIDs = [...source.matchAll(/ID:\s*"([a-z_]+)"/g)].map((m) => m[1]);
    expect(goIDs).toEqual(environmentSlots.map((slot) => slot.id));
  });

  test("renders every slot, so an unfilled one says what the rules fall back to", () => {
    const rendered = renderEnvironment({ slots: { domains: ["grafana.acme.corp"] }, notes: [] });
    expect(rendered).toContain("**Trusted internal domains**: grafana.acme.corp");
    expect(rendered).toContain("**Internal package registry**: None configured");
    for (const slot of environmentSlots) expect(rendered).toContain(`**${slot.label}**:`);
  });

  test("notes render apart from the slots, marked as context", () => {
    const rendered = renderEnvironment({ slots: {}, notes: ["the CI box shares this checkout"] });
    expect(rendered).toContain("It is context, not a list");
    expect(rendered).toContain("> the CI box shares this checkout");
  });

  test("reading drops blank entries and keeps what a slot holds", () => {
    const env = readEnvironment({ slots: { domains: ["  a.corp  ", "", "  "] }, notes: ["x", 7] });
    expect(env.slots.domains).toEqual(["a.corp"]);
    expect(env.notes).toEqual(["x"]);
  });

  test("prose where the document belongs is refused by name", () => {
    expect(() => readEnvironment("a work laptop")).toThrow(/slots and notes/);
  });
});
