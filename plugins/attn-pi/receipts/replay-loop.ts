import { ModelRegistry, ModelRuntime } from "@earendil-works/pi-coding-agent";
import { readFileSync } from "node:fs";
import { classifierCacheRetention, intentMaxTokens } from "../automode/model-classifier";
import { parseSeverity } from "../automode/prompt";
const [at, n = "8", spec = "opencode-go/glm-5.3-flash", reasoning = "minimal", variant = "asis"] = process.argv.slice(2);
const rec = readFileSync("/Users/victor/.attn/attn-automode-denials.jsonl", "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l)).find((r) => r.at.startsWith(at))!;
let user: string = rec.prompt.user;
import intent from "../../../internal/prompts/content/pi/intent.md" with { type: "text" };
// "current" swaps the recorded pass-2 closing instruction for the one in the checkout.
if (variant === "current") user = user.replace(/This is pass 2\.[\s\S]*$/, intent.trim());
const runtime = await ModelRuntime.create(); const registry = new ModelRegistry(runtime); await registry.refresh();
const [prov, ...rest] = spec.split("/"); const model = registry.find(prov, rest.join("/"))!;
const provider = registry.getProvider(model.provider)!; const auth = await registry.getApiKeyAndHeaders(model); if (!auth.ok) throw new Error(auth.error);
const baseUrl = (await registry.getProviderAuth(model.provider))?.auth?.baseUrl;
let bad = 0;
for (let i = 0; i < Number(n); i++) {
  const r = await provider.streamSimple(baseUrl ? { ...model, baseUrl } : model,
    { systemPrompt: rec.prompt.system, messages: [{ role: "user", content: [{ type: "text", text: user }], timestamp: Date.now() }] },
    { ...(reasoning === "none" ? {} : { reasoning }), maxTokens: intentMaxTokens, cacheRetention: classifierCacheRetention, apiKey: auth.apiKey, headers: auth.headers, env: auth.env }).result();
  const text = (r.content ?? []).filter((b: any) => b.type === "text").map((b: any) => b.text).join("");
  const p = parseSeverity(text); if (!p) bad++; const withReason = p?.thinking ? "reason" : "bare";
  console.log(`#${i} stop=${r.stopReason} out=${r.usage?.output} blocks=${(r.content ?? []).map((b: any) => b.type + ":" + (b.text ?? b.thinking ?? "").length).join(",")} parsed=${p ? p.severity + " " + withReason : "UNREADABLE"} err=${r.errorMessage ?? ""}${p ? "" : " TEXT>" + JSON.stringify(text.slice(0, 200))}`);
}
console.log(`=== ${spec} reasoning=${reasoning} variant=${variant}: ${bad}/${n} unreadable`);
