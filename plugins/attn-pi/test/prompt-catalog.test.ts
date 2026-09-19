import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import catalog from "../automode/prompts.generated.json";
import { renderPrompt } from "../automode/prompt-catalog";

describe("Go and Pi prompt rendering", () => {
  for (const fixture of catalog.parity) {
    test(`${fixture.recipient}/${fixture.event} ${JSON.stringify(fixture.values)}`, () => {
      const text = renderPrompt(
        fixture.event,
        fixture.values,
        fixture.recipient,
      );
      expect(createHash("sha256").update(text).digest("hex")).toBe(
        fixture.sha256,
      );
    });
  }
  test("rejects undeclared and missing inputs", () => {
    expect(() => renderPrompt("configuration-error", {}, "pi-security")).toThrow("Missing prompt input");
    expect(() =>
      renderPrompt("configuration-error", { problem: "no config", typo: "ignored?" }, "pi-security"),
    ).toThrow("Unknown prompt input");
    expect(() => renderPrompt("unknown", {}, "pi-security")).toThrow("Unknown prompt");
  });
});
