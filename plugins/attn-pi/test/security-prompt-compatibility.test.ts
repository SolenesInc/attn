import { expect, test } from "bun:test";
import fixtures from "./testdata/security-prompts.json";
import { securityInstructions, writeRecovery, sandboxRecovery, reviewUnavailable } from "../security/guidance";
import { sandboxDenialToolResult, type Denial } from "../automode/denial";
import type { SecurityPolicy } from "../security/policy";

for (const [index, fixture] of fixtures.entries()) {
  test(`preserves original ${fixture.kind} prompt ${index}`, () => {
    const input = fixture.input as { policy: SecurityPolicy; review: boolean; failure: "permission" | "network" };
    let actual: string;
    switch (fixture.kind) {
      case "security": actual = securityInstructions(input.policy, input.review); break;
      case "write-recovery": actual = writeRecovery(input.policy, input.review); break;
      case "sandbox-recovery": actual = sandboxRecovery(input.policy, input.review, input.failure); break;
      case "sandbox-denial": actual = sandboxDenialToolResult(fixture.input as Denial); break;
      case "review-unavailable": actual = reviewUnavailable; break;
      default: throw new Error(`Unknown fixture kind: ${fixture.kind}`);
    }
    expect(actual).toBe(fixture.expected);
  });
}
