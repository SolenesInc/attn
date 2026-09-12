import { expect, test } from "bun:test";
import { readGuardianSelection, resolveGuardian } from "../approval/guardian-selection";
import { defaultApprovalConfig, loadApprovalConfig } from "../approval/config";
import { PiApproval } from "../approval/session";

const coding = { provider: "one", id: "coding", reasoning: true };
const other = { provider: "two", id: "review", reasoning: true, thinkingLevelMap: { xhigh: "high" } };
const models = [coding, other, { provider: "two", id: "plain", reasoning: false }];
function context() {
  return { model: coding, modelRegistry: {
    find: (provider: string, id: string) => models.find(m => m.provider === provider && m.id === id),
    getAvailable: () => models,
    getApiKeyAndHeaders: async () => ({ ok: true }),
  }, ui: { notify: () => {}, setStatus: () => {} } } as any;
}

test("launch selection validates provider/model pairs and defaults old configurations", () => {
  expect(loadApprovalConfig({}).guardian).toEqual({});
  expect(loadApprovalConfig({ guardian: { provider: "two", model: "review", effort: "high" } }).guardian.effort).toBe("high");
  for (const raw of [null, [], { provider: "two" }, { model: "review" }, { effort: "ultra" }, { provider: "a b", model: "m" }]) expect(() => readGuardianSelection(raw)).toThrow();
});

test("follow mode resolves the current coding model and explicit selection stays independent", () => {
  const ctx = context();
  expect(resolveGuardian({}, ctx)).toEqual({ model: coding, effort: "low" });
  expect(resolveGuardian({ provider: "two", model: "review", effort: "xhigh" }, ctx)).toEqual({ model: other, effort: "xhigh" });
  ctx.model = models[2];
  expect(resolveGuardian({}, ctx)).toEqual({ model: models[2], effort: undefined });
  expect(() => resolveGuardian({ effort: "high" }, ctx)).toThrow("supported: off");
  expect(() => resolveGuardian({ provider: "missing", model: "model" }, ctx)).toThrow("missing/model");
});

test("session override resets explicitly and at reload without changing launch defaults", async () => {
  const approval = new PiApproval({ config: { ...defaultApprovalConfig, guardian: { provider: "two", model: "review" } }, suite: {} as any, ledger: { record: () => {} } });
  const ctx = context();
  const handlers = new Map<string, Function>();
  approval.register({ registerFlag: () => {}, registerCommand: () => {}, getFlag: () => undefined, on: (name: string, fn: Function) => handlers.set(name, fn) } as any);
  const control = approval.guardianControl;
  expect(control.snapshot(ctx).source).toBe("attn default");
  await control.change("model session", ctx);
  await control.change("effort high", ctx);
  expect(control.snapshot(ctx).effective).toBe("one/coding · effort high");
  expect(control.snapshot(ctx).source).toBe("session override");
  await expect(control.change("model two plain", ctx)).rejects.toThrow("supported: off");
  expect(control.snapshot(ctx).selection).toEqual({ effort: "high" });
  await control.change("reset", ctx);
  expect(control.snapshot(ctx).selection).toEqual({ provider: "two", model: "review" });
  await control.change("effort high", ctx);
  await handlers.get("session_start")!({}, ctx);
  expect(control.snapshot(ctx).source).toBe("attn default");
  expect(control.snapshot(ctx).selection).toEqual({ provider: "two", model: "review" });
});

test("missing credentials reject the override and preserve the existing selection", async () => {
  const approval = new PiApproval({ config: defaultApprovalConfig, suite: {} as any, ledger: { record: () => {} } });
  const ctx = context();
  ctx.modelRegistry.getApiKeyAndHeaders = async () => ({ ok: false, error: "credentials missing" });
  await expect(approval.guardianControl.change("model two review", ctx)).rejects.toThrow("credentials missing");
  expect(approval.guardianControl.snapshot(ctx).source).toBe("attn default");
});
