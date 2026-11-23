import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, expect } from "@playwright/test";

const repo = fileURLToPath(new URL("../../..", import.meta.url));
const fixture = await fs.mkdtemp(path.join(os.tmpdir(), "prompt-editor-sheet-"));
const artifacts = process.env.PROMPT_EDITOR_ARTIFACTS;
const git = (...args) => execFileSync("git", ["-C", fixture, ...args], { encoding: "utf8", env: { ...process.env, GIT_CONFIG_GLOBAL: os.devNull, GIT_CONFIG_NOSYSTEM: "1" } }).trim();
const root = path.join(fixture, "internal/prompts");
const source = (name) => path.join(root, "content", name);
const read = (name) => fs.readFile(source(name), "utf8");
const placeholders = (text) => [...text.matchAll(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g)].map((match) => match[0]);
const commit = (message) => {
  git("add", ".");
  git("-c", "user.name=Sheet test", "-c", "user.email=sheet@example.test", "commit", "--no-gpg-sign", "--allow-empty", "-m", message);
};
let server, browser, context;
const pageErrors = [];
try {
  await fs.access(path.join(repo, "cmd/prompt-editor/web/sheet/dist/sheet.js")).catch(() => {
    throw new Error("The Sheet bundle is missing: run node cmd/prompt-editor/build.mjs first");
  });
  await fs.mkdir(root, { recursive: true });
  for (const name of ["content", "scenarios", "situations"])
    await fs.cp(path.join(repo, "internal/prompts", name), path.join(root, name), { recursive: true });
  const manifest = JSON.parse(await fs.readFile(path.join(repo, "internal/prompts/catalog.generated.json"), "utf8"));
  delete manifest.definitions_hash;
  await fs.writeFile(path.join(root, "catalog.generated.json"), JSON.stringify(manifest));
  git("init", "-b", "main");
  commit("Prompt fixture");
  const binary = process.env.PROMPT_EDITOR_BIN || path.join(fixture, "prompt-editor");
  if (!process.env.PROMPT_EDITOR_BIN) execFileSync("go", ["build", "-o", binary, "./cmd/prompt-editor"], { cwd: repo, stdio: "inherit" });
  server = spawn(binary, ["--repo", fixture], { stdio: ["ignore", "pipe", "pipe"] });
  const url = await new Promise((resolve, reject) => {
    let stdout = "";
    server.once("error", reject);
    server.once("exit", (code) => reject(new Error(`Editor exited ${code}`)));
    server.stdout.on("data", (chunk) => {
      stdout += chunk;
      const match = stdout.match(/http:\/\/127\.0\.0\.1:\d+/);
      if (match) resolve(match[0]);
    });
    server.stderr.on("data", (chunk) => process.stderr.write(chunk));
  });
  if (artifacts) await fs.mkdir(artifacts, { recursive: true });
  browser = await chromium.launch({ headless: true });
  context = await browser.newContext({ colorScheme: "light", viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  page.on("pageerror", (error) => pageErrors.push(String(error)));
  page.on("console", (message) => { if (message.type() === "error" && !message.text().startsWith("Failed to load resource")) pageErrors.push(message.text()); });
  const saveState = page.locator("#save-state");
  const row = (name, scope = page) => scope.locator(`.row[data-path="content/${name}"]`);
  const editor = (name, scope = page) => row(name, scope).locator(".cm-content");

  await page.goto(`${url}/sheet/#situation=crew-member-start`);
  await expect(page.locator(".event h2")).toHaveText(["session/launch", "session/garden-ready", "crew/new-session"]);
  await expect(page.locator("#title-id")).toHaveText("crew-member-start");

  // C6: type beside a chip, undo, then type again and save.
  const identity = "crew/v2/identity.md";
  const identityBefore = await read(identity);
  const chip = editor(identity).locator('.chip[data-name="display_name"]');
  await expect(chip).toHaveCount(1);
  await chip.click();
  await page.keyboard.type("Q");
  await expect(saveState).toHaveText(/1 unsaved file/);
  await expect(row(identity).locator(".unsaved")).toBeVisible();
  await page.keyboard.press("ControlOrMeta+z");
  await expect(saveState).toHaveText("All saved");
  await expect(chip).toHaveCount(1);
  await expect(row(identity).locator(".unsaved")).toBeHidden();
  await chip.click();
  await page.keyboard.type("Z");
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  const identityAfter = await read(identity);
  assert.ok(
    identityAfter === identityBefore.replace("{{display_name}}", "Z{{display_name}}") || identityAfter === identityBefore.replace("{{display_name}}", "{{display_name}}Z"),
    `typed text lands beside the chip: ${JSON.stringify(identityAfter.slice(0, 80))}`,
  );
  assert.deepEqual(placeholders(identityAfter), placeholders(identityBefore));
  commit("C6 save");

  // C7: expand a stub and edit the fragment it hides.
  const charterStub = page.locator(".stub", { hasText: "unless has_charter" });
  await charterStub.click();
  const hidden = charterStub.locator("xpath=ancestor::div[contains(@class,'stub-holder')]").locator(".stub-body .row").first();
  const hiddenPath = (await hidden.getAttribute("data-path")).replace(/^content\//, "");
  const hiddenBefore = await read(hiddenPath);
  await hidden.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+Home");
  await page.keyboard.type("Untaken edit. ");
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  assert.equal(await read(hiddenPath), "Untaken edit. " + hiddenBefore);
  commit("C7 untaken save");

  // C7: a flip delivers the other branch and writes nothing.
  await expect(page.locator(`.row:not(.untaken)[data-path="content/${hiddenPath}"]`)).toHaveCount(0);
  await page.locator(".flip", { hasText: "flip has_charter" }).first().click();
  await expect(page.locator(`.row:not(.untaken)[data-path="content/${hiddenPath}"]`)).toHaveCount(1);
  await expect(editor(hiddenPath)).toContainText("Untaken edit.");
  await expect(page.locator(".flip[aria-pressed=true]").first()).toContainText("has_charter flipped");
  assert.equal(git("status", "--porcelain"), "");
  await page.locator(".flip[aria-pressed=true]").first().click();
  await expect(page.locator(`.row:not(.untaken)[data-path="content/${hiddenPath}"]`)).toHaveCount(0);

  // C7: a file that appears twice follows typing in both places.
  const wake = "crew/v2/new-session.md";
  const wakeEvent = page.locator('.event[data-event="crew/new-session"]');
  await wakeEvent.locator(".stub", { hasText: "if start_cause present" }).click();
  await wakeEvent.locator(".stub", { hasText: "unless charter_decides" }).click();
  await expect(row(wake, wakeEvent)).toHaveCount(2);
  await expect(row(wake, wakeEvent).first().locator(".twice")).toHaveText("×2 on this page");
  await editor(wake, wakeEvent).first().click();
  await page.keyboard.press("ControlOrMeta+Home");
  await page.keyboard.type("Twice. ");
  await expect(editor(wake, wakeEvent).nth(0)).toContainText("Twice. This is a new session.");
  await expect(editor(wake, wakeEvent).nth(1)).toContainText("Twice. This is a new session.");

  // C7: another situation from the palette keeps unsaved edits.
  await page.keyboard.press("ControlOrMeta+k");
  await page.getByLabel("Open a situation, scenario or event").fill("delegate-start");
  await page.keyboard.press("Enter");
  await expect(page.locator("#title-id")).toHaveText("delegate-start");
  await expect(page.locator(".event h2")).toHaveText(["session/launch", "session/garden-ready", "delegation/opening"]);
  await expect(saveState).toHaveText(/1 unsaved file/);
  await page.keyboard.press("ControlOrMeta+k");
  await page.getByLabel("Open a situation, scenario or event").fill("situation crew-member");
  await page.keyboard.press("Enter");
  await expect(page.locator("#title-id")).toHaveText("crew-member-start");
  await expect(editor(wake, wakeEvent)).toContainText("Twice. This is a new session.");
  assert.ok(!(await read(wake)).startsWith("Twice."));
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  assert.ok((await read(wake)).startsWith("Twice. This is a new session."));
  commit("C7 twice save");

  // A file changed on disk since load is a conflict: the local text stays unsaved and the disk keeps its text.
  const cleared = "crew/cleared.md";
  await editor(cleared).click();
  await page.keyboard.press("ControlOrMeta+Home");
  await page.keyboard.type("Mine. ");
  await fs.writeFile(source(cleared), "Changed by another process.\n");
  await page.keyboard.press("ControlOrMeta+s");
  await expect(row(cleared).locator(".conflict")).toBeVisible();
  await expect(saveState).toHaveText(/1 unsaved file, 1 in conflict/);
  assert.equal(await read(cleared), "Changed by another process.\n");
  await expect(editor(cleared)).toContainText("Mine. ");
  await fs.writeFile(source(cleared), (await fs.readFile(path.join(repo, "internal/prompts/content", cleared), "utf8")));

  // A save that fails midway keeps the files written before the failure saved.
  await page.goto("about:blank");
  await page.goto(`${url}/sheet/#situation=crew-member-start`);
  await expect(saveState).toHaveText("All saved");
  const capture = "session/quick-capture.md";
  await editor(cleared).click();
  await page.keyboard.press("ControlOrMeta+Home");
  await page.keyboard.type("First. ");
  await editor(capture).click();
  await page.keyboard.press("ControlOrMeta+Home");
  await page.keyboard.type("Second. ");
  await fs.chmod(path.join(root, "content/session"), 0o555);
  try {
    await page.keyboard.press("ControlOrMeta+s");
    await expect(page.locator("#notice")).toContainText("Save failed");
    assert.ok(!(await read(capture)).startsWith("Second. "));
    for (const [name, typed] of [[cleared, "First. "], [capture, "Second. "]]) {
      const written = (await read(name)).startsWith(typed);
      await expect(row(name).locator(".unsaved")).toBeVisible({ visible: !written });
    }
  } finally {
    await fs.chmod(path.join(root, "content/session"), 0o755);
  }
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  assert.ok((await read(capture)).startsWith("Second. "));

  // An unsaved edit the catalog would refuse does not stop another page from opening.
  await editor(cleared).click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type(" {{oops}}");
  await page.keyboard.press("ControlOrMeta+k");
  await page.getByLabel("Open a situation, scenario or event").fill("delegate-start");
  await page.keyboard.press("Enter");
  await expect(page.locator("#title-id")).toHaveText("delegate-start");
  await expect(page.locator(".event h2")).toHaveText(["session/launch", "session/garden-ready", "delegation/opening"]);
  await expect(saveState).toHaveText(/1 unsaved file/);

  // A scenario or a bare event opens from its hash.
  await page.goto(`${url}/sheet/#scenario=crew-v2-new-session`);
  await expect(page.locator(".event h2")).toHaveText(["crew/new-session"]);
  await page.goto(`${url}/sheet/#event=session/garden-ready`);
  await expect(page.locator(".event h2")).toHaveText(["session/garden-ready"]);

  // Leaving with unsaved text asks first.
  await editor("session/garden-ready.md").click();
  await page.keyboard.type("x");
  const asked = new Promise((resolve) => page.once("dialog", (dialog) => { resolve(dialog.type()); dialog.dismiss(); }));
  await page.close({ runBeforeUnload: true });
  assert.equal(await asked, "beforeunload");
  assert.deepEqual(pageErrors, []);
  console.log(JSON.stringify({ status: "passed", fixture: path.basename(fixture) }));
} finally {
  if (context) await context.close();
  if (browser) await browser.close();
  if (server && server.exitCode === null) { server.kill("SIGTERM"); await once(server, "exit"); }
  await fs.rm(fixture, { recursive: true, force: true });
}
