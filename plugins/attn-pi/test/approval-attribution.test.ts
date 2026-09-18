import { expect, test } from "bun:test";
import { SettingsManager } from "@earendil-works/pi-coding-agent";
import { createAttributionHeaders, guardianAttributionSettings } from "../approval/attribution";

const settings = SettingsManager.create(process.cwd(), process.env.PI_CODING_AGENT_DIR);
const headers = createAttributionHeaders(settings);

test("an opencode model gets pi's session headers with the auth headers preserved", () => {
  expect(headers(
    { provider: "opencode-go", id: "glm-5.3", baseUrl: "https://opencode.ai/zen/go/v1" },
    "session-7",
    { "x-auth": "key" },
  )).toEqual({ "x-opencode-session": "session-7", "x-opencode-client": "pi", "x-auth": "key" });
});

test("providers pi does not attribute send only their auth headers", () => {
  expect(headers({ provider: "anthropic", id: "claude-test", baseUrl: "https://api.anthropic.com" }, "session-7", { "x-auth": "key" }))
    .toEqual({ "x-auth": "key" });
});

test("an empty session id suppresses the session headers", () => {
  expect(headers({ provider: "opencode-go", id: "glm-5.3", baseUrl: "https://opencode.ai/zen/go/v1" }, "", { "x-auth": "key" }))
    .toEqual({ "x-auth": "key" });
});

test("a missing baseUrl reads as an empty one instead of breaking the attribution lookup", () => {
  expect(headers({ provider: "opencode-go", id: "glm-5.3" }, "session-7", undefined))
    .toEqual({ "x-opencode-session": "session-7", "x-opencode-client": "pi" });
});

test("the settings come from the session cwd and pi's agent dir", () => {
  expect(guardianAttributionSettings(process.cwd())).toBeInstanceOf(SettingsManager);
});
