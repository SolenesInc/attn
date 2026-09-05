import { describe, expect, it } from "vitest";
import terminal from "./testdata/terminal.json";
import labels from "./testdata/quick-labels.json";
import catalog from "./catalog.generated.json";
import { renderPrompt } from "./render";
import { buildAnnotationPayload } from "../components/TerminalAnnotations/quickLabels";
import { QUICK_LABELS } from "../annotations/quickLabels";

describe("original annotation prompts", () => {
  terminal.forEach((fixture, index) => {
    it(`preserves terminal feedback ${index}`, () => {
      expect(buildAnnotationPayload(fixture.annotations, fixture.note)).toBe(
        fixture.expected,
      );
    });
  });
  it("preserves every built-in quick label", () => {
    expect(QUICK_LABELS).toEqual(labels);
  });
});

describe("Go and frontend rendering agree", () => {
  catalog.parity.forEach((fixture, index) => {
    it(`renders ${fixture.recipient}/${fixture.event} case ${index}`, async () => {
      const text = renderPrompt(
        fixture.recipient,
        fixture.event,
        fixture.values as Record<string, string>,
      );
      const digest = await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(text),
      );
      const hash = Array.from(new Uint8Array(digest), (byte) =>
        byte.toString(16).padStart(2, "0"),
      ).join("");
      expect(hash).toBe(fixture.sha256);
    });
  });
});
