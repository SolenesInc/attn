import { afterAll, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { resumeAvailable, resumeAvailabilityBatch } from "../src/sessions";

const root = mkdtempSync(join(tmpdir(), "pi-sessions-"));
afterAll(() => rmSync(root, { recursive: true, force: true }));

// Pi's --session-id selects an exact header ID in its cwd's storage; a custom
// directory additionally filters by header cwd. Inspection never creates storage.
for (const scenario of [
  { name: "missing directory", directory: false, available: false },
  { name: "empty directory", available: false },
  { name: "unreadable project settings", unreadableSettings: "project", available: true },
  { name: "unreadable global settings", unreadableSettings: "global", available: true },
  { name: "unreadable non-target candidate", unreadable: true, available: true },
  { name: "large multibyte header", headerPadding: "λ".repeat(4096), available: true },
  { name: "renamed file", filename: "arbitrary.jsonl", available: true },
  { name: "filename only matches", headerID: "other", available: false },
  { name: "prefix only matches", headerID: "conversation-longer", available: false },
  { name: "wrong header type", type: "message", available: false },
  { name: "malformed lines before header", prefix: "\ninvalid\n", available: true },
  { name: "valid entry before header", prefix: '{"type":"message"}\n', available: false },
  { name: "ignored extension", filename: "conversation.txt", available: false },
  { name: "unterminated header", newline: false, available: true },
  { name: "relative agent directory", relativeAgent: true, available: true },
  { name: "environment session directory", source: "env", available: true },
  { name: "global session directory", source: "global", available: true },
  { name: "project session directory", source: "project", available: true },
  { name: "project clears global directory", source: "clear", available: true },
  { name: "project null clears global directory", source: "null", available: true },
  { name: "environment overrides settings", source: "override", available: true },
  { name: "custom directory wrong cwd", source: "env", headerCwd: "/other", available: false },
  { name: "custom directory lacks cwd", source: "env", headerCwd: null, available: false },
] as const) {
  test(`Pi resume storage: ${scenario.name}`, async () => {
    const caseRoot = join(root, scenario.name);
    const cwd = join(caseRoot, "project:with-colon");
    const agentDir = join(cwd, "agent");
    mkdirSync(agentDir, { recursive: true });
    const env: Record<string, string> = { PI_CODING_AGENT_DIR: "relativeAgent" in scenario ? "agent" : agentDir };
    let directory = join(agentDir, "sessions", `--${cwd.slice(1).replaceAll("/", "-").replaceAll(":", "-")}--`);
    if ("source" in scenario) {
      const projectSettings = join(cwd, ".pi");
      mkdirSync(projectSettings);
      if (scenario.source === "global") {
        writeFileSync(join(agentDir, "settings.json"), '{"sessionDir":"custom"}');
      } else if (scenario.source === "project") {
        writeFileSync(join(agentDir, "settings.json"), '{"sessionDir":"wrong"}');
        writeFileSync(join(projectSettings, "settings.json"), '{"sessionDir":"custom"}');
      } else if (scenario.source === "clear" || scenario.source === "null") {
        writeFileSync(join(agentDir, "settings.json"), '{"sessionDir":"wrong"}');
        writeFileSync(join(projectSettings, "settings.json"), JSON.stringify({ sessionDir: scenario.source === "null" ? null : "" }));
      } else {
        env.PI_CODING_AGENT_SESSION_DIR = "custom";
        if (scenario.source === "override") writeFileSync(join(projectSettings, "settings.json"), '{"sessionDir":"wrong"}');
      }
      if (scenario.source !== "clear" && scenario.source !== "null") directory = join(cwd, "custom");
    }
    if (!("directory" in scenario)) mkdirSync(directory, { recursive: true });
    if (scenario.name !== "missing directory" && scenario.name !== "empty directory") {
      const header = {
        type: "type" in scenario ? scenario.type : "session",
        id: "headerID" in scenario ? scenario.headerID : "conversation",
        cwd: "headerCwd" in scenario ? scenario.headerCwd : cwd,
        ...("headerPadding" in scenario ? { padding: scenario.headerPadding } : {}),
      };
      writeFileSync(join(directory, "filename" in scenario ? scenario.filename : "timestamp_conversation.jsonl"),
        ("prefix" in scenario ? scenario.prefix : "") + JSON.stringify(header) + ("newline" in scenario ? "" : "\n"));
    }
    if ("unreadable" in scenario) mkdirSync(join(directory, "00-unreadable.jsonl"));
    if ("unreadableSettings" in scenario) {
      const settingsDir = scenario.unreadableSettings === "project" ? join(cwd, ".pi") : agentDir;
      mkdirSync(join(settingsDir, "settings.json"), { recursive: true });
    }
    const result = await resumeAvailable({ cwd, resume_session_id: "conversation" }, env);
    expect(result.available).toBe(scenario.available);
    if (!result.available) expect(result.reason).toContain(`conversation conversation is no longer in pi's storage (${directory})`);
    if (scenario.name === "missing directory") {
      const { existsSync } = await import("node:fs");
      expect(existsSync(directory)).toBe(false);
    }
  });
}


test("Pi batch availability is fresh and matches each ID in shared custom storage", async () => {
  const caseRoot = join(root,"batch");
  const first = join(caseRoot,"first");
  const second = join(caseRoot,"second");
  const directory = join(caseRoot,"sessions");
  mkdirSync(first,{recursive:true}); mkdirSync(second,{recursive:true}); mkdirSync(directory,{recursive:true});
  const env = { PI_CODING_AGENT_DIR:join(caseRoot,"agent"), PI_CODING_AGENT_SESSION_DIR:directory };
  const write = (filename:string,id:string,cwd:string) => writeFileSync(join(directory,filename),JSON.stringify({type:"session",id,cwd})+"\n");
  write("a.jsonl","same-id",first); write("b.jsonl","second-id",first); write("c.jsonl","same-id",second);
  const params = { conversations: [
    {agent:"pi",cwd:first,resume_session_id:"same-id"},
    {agent:"pi",cwd:first,resume_session_id:"second-id"},
    {agent:"pi",cwd:first,resume_session_id:"missing"},
    {agent:"pi",cwd:second,resume_session_id:"same-id"},
  ] };
  const initial = await resumeAvailabilityBatch(params,env);
  expect(initial.availability.map(answer => answer.available)).toEqual([true,true,false,true]);
  expect(initial.availability.map(({agent,cwd,resume_session_id}) => ({agent,cwd,resume_session_id}))).toEqual(params.conversations);
  rmSync(join(directory,"a.jsonl"));
  const next = await resumeAvailabilityBatch(params,env);
  expect(next.availability.map(answer => answer.available)).toEqual([false,true,false,true]);
});
