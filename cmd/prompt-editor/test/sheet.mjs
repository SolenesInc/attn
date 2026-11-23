import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, expect } from "@playwright/test";
import { merge3, mergeKeepingMine } from "../web/sheet/merge.js";

// C15: merge3 against its rules. Each side's edits are base-relative line ranges; two ranges overlap when they share a base line, or are insertions at the same point. Disjoint edits merge, adjacent ones included; overlapping, different edits conflict, and keeping mine keeps the disk's other edits.
{
  let seed = 1;
  const random = (below) => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed % below;
  };
  const overlaps = (a, b) => (a.from === a.to && b.from === b.to ? a.from === b.from : a.from < b.to && b.from < a.to);
  const sameEdit = (a, b) => a.from === b.from && a.to === b.to && a.lines.join() === b.lines.join();
  const apply = (lines, edits) => {
    const out = [];
    let at = 0;
    for (const edit of [...edits].sort((a, b) => a.from - b.from || a.to - b.to)) {
      out.push(...lines.slice(at, edit.from), ...edit.lines);
      at = edit.to;
    }
    return [...out, ...lines.slice(at)].join("\n");
  };
  let tokens = 0;
  const editWithin = (from, end, side) => {
    for (;;) {
      const start = from + random(end - from + 1);
      const edit = { from: start, to: start + random(end - start + 1), lines: Array.from({ length: random(3) }, () => `${side}${tokens++}`) };
      if (edit.from < edit.to || edit.lines.length) return edit;
    }
  };
  for (let run = 0; run < 3000; run++) {
    const base = Array.from({ length: random(9) }, (_, index) => `line ${index}`);
    if (base.length && random(2)) base.push("");
    const baseText = base.join("\n");
    const mine = editWithin(0, base.length, "mine ");
    const theirs = [random(5) ? editWithin(0, base.length, "disk ") : mine];
    if (theirs[0].to + 1 < base.length && random(2)) theirs.push(editWithin(theirs[0].to + 1, base.length, "disk "));
    const kept = theirs.filter((edit) => !sameEdit(edit, mine));
    const apart = kept.filter((edit) => !overlaps(edit, mine));
    const cases = { baseText, mine, theirs };
    const merged = merge3(baseText, apply(base, [mine]), apply(base, theirs));
    if (apart.length === kept.length) assert.deepEqual(merged, { conflict: false, text: apply(base, [mine, ...kept]) }, JSON.stringify(cases));
    else assert.deepEqual(merged, { conflict: true }, JSON.stringify(cases));
    assert.equal(mergeKeepingMine(baseText, apply(base, [mine]), apply(base, theirs)), apply(base, [mine, ...apart]), JSON.stringify(cases));
  }
}

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
  const cdp = await context.newCDPSession(page);
  let viewPrototype;
  const liveEditorViews = async () => {
    await cdp.send("HeapProfiler.collectGarbage");
    viewPrototype ??= (await cdp.send("Runtime.evaluate", { expression: 'Object.getPrototypeOf(document.querySelector("#document .cm-content").cmTile.root.view)' })).result.objectId;
    const { objects } = await cdp.send("Runtime.queryObjects", { prototypeObjectId: viewPrototype });
    const { result } = await cdp.send("Runtime.callFunctionOn", { objectId: objects.objectId, functionDeclaration: "function () { return this.length; }", returnByValue: true });
    await cdp.send("Runtime.releaseObject", { objectId: objects.objectId });
    return result.value;
  };
  const expectOnlyMountedViews = async () => assert.equal(await liveEditorViews(), await page.locator("#document .cm-editor").count());
  const saveState = page.locator("#save-state");
  const row = (name, scope = page) => scope.locator(`.row[data-path="content/${name}"]`);
  const editor = (name, scope = page) => row(name, scope).locator(".cm-content");

  const show = (...args) => JSON.parse(execFileSync(binary, ["show", ...args, "--repo", fixture, "--json"], { encoding: "utf8" })).url;
  const situationLink = show("--situation", "crew-member-start");
  assert.equal(situationLink, `${url}/#situation=crew-member-start`);
  await page.goto(situationLink);
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

  // C14: switching pages frees the editors it discards and keeps unsaved text.
  for (const id of ["delegate-start", "crew-member-start", "delegate-start", "crew-member-start"]) {
    await page.keyboard.press("ControlOrMeta+k");
    await page.getByLabel("Open a situation, scenario or event").fill(`situation ${id}`);
    await page.keyboard.press("Enter");
    await expect(page.locator("#title-id")).toHaveText(id);
  }
  await expect(editor(wake, wakeEvent)).toContainText("Twice. This is a new session.");
  await expect(saveState).toHaveText(/1 unsaved file/);
  await expectOnlyMountedViews();
  await page.evaluate(() => { location.hash = ""; });
  await expect(page.locator("#document .empty")).toBeVisible();
  assert.equal(await liveEditorViews(), 0);
  await page.getByLabel("Open a situation, scenario or event").fill("situation crew-member-start");
  await page.keyboard.press("Enter");
  await expect(page.locator("#title-id")).toHaveText("crew-member-start");
  await expect(editor(wake, wakeEvent)).toContainText("Twice. This is a new session.");
  assert.ok(!(await read(wake)).startsWith("Twice."));
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  assert.ok((await read(wake)).startsWith("Twice. This is a new session."));
  commit("C7 twice save");

  // C9: a file another process changes merges into the open page.
  await expect(page.locator("body")).toHaveAttribute("data-watching", "true");
  const capture = "session/quick-capture.md";
  const changeLine = async (index, edit) => {
    const lines = (await read(capture)).split("\n");
    lines[index] = edit(lines[index]);
    await fs.writeFile(source(capture), lines.join("\n"));
    return lines.join("\n");
  };
  const typeAtStart = async (name, text) => {
    await editor(name).click();
    await page.keyboard.press("ControlOrMeta+Home");
    await page.keyboard.type(text);
  };
  await changeLine(5, (line) => line + " Disk one.");
  await expect(editor(capture)).toContainText("Disk one.");
  await expect(saveState).toHaveText("All saved");

  await typeAtStart(capture, "Local. ");
  const diskTwo = await changeLine(5, (line) => line + " Disk two.");
  await expect(editor(capture)).toContainText("Disk two.");
  await expect(editor(capture)).toContainText("Local. ");
  await expect(saveState).toHaveText(/1 unsaved file$|1 unsaved file ·/);
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  assert.equal(await read(capture), "Local. " + diskTwo);

  // C15: edits on adjacent lines merge and both are saved.
  await typeAtStart(capture, "Near. ");
  const adjacent = await changeLine(1, (line) => line + " Next line.");
  await expect(editor(capture)).toContainText("Next line.");
  await expect(row(capture).locator(".conflict")).toBeHidden();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  assert.equal(await read(capture), "Near. " + adjacent);

  // C15: an edit on the line I changed conflicts; Keep mine keeps my line and the disk's change elsewhere.
  await typeAtStart(capture, "Mine. ");
  await changeLine(0, (line) => "Theirs. " + line);
  const theirs = await changeLine(4, (line) => line + " Elsewhere.");
  await expect(row(capture).locator(".conflict")).toBeVisible();
  await expect(row(capture).locator(".conflict-disk")).toContainText("Theirs. Near. Local. ");
  await expect(saveState).toHaveText(/1 in conflict/);
  if (artifacts) await row(capture).screenshot({ path: path.join(artifacts, "conflict.png") });
  await page.keyboard.press("ControlOrMeta+s");
  await expect(page.locator("#notice")).toContainText("Save skips them");
  assert.equal(await read(capture), theirs);
  await row(capture).getByRole("button", { name: "Keep mine" }).click();
  await expect(row(capture).locator(".conflict")).toBeHidden();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  const kept = theirs.split("\n");
  kept[0] = kept[0].replace("Theirs. ", "Mine. ");
  assert.equal(await read(capture), kept.join("\n"));
  await expect(editor(capture)).toContainText("Elsewhere.");

  await typeAtStart(capture, "Dropped. ");
  const disk = await changeLine(0, (line) => "Disk wins. " + line);
  await row(capture).getByRole("button", { name: "Use disk" }).click();
  await expect(editor(capture)).toContainText("Disk wins. Mine. ");
  await expect(editor(capture)).not.toContainText("Dropped.");
  await expect(saveState).toHaveText("All saved");
  assert.equal(await read(capture), disk);
  commit("C9 disk changes");

  // C9: the diff shows a changed fragment's change and folds unchanged fragments.
  const original = (await read(capture)).split("\n")[2];
  await changeLine(2, (line) => line + " Diffed.");
  await expect(editor(capture)).toContainText("Diffed.");
  await page.keyboard.press("ControlOrMeta+Shift+d");
  await expect(page.locator("#diff")).toHaveAttribute("aria-pressed", "true");
  await expect(row(capture)).not.toHaveClass(/folded/);
  await expect(row(capture).locator(".cm-added")).toHaveText([original + " Diffed."]);
  await expect(row(capture).locator(".cm-removed")).toHaveText([original]);
  await expect(row(identity)).toHaveClass(/folded/);
  await expect(editor(identity)).toBeHidden();
  if (artifacts) await page.screenshot({ path: path.join(artifacts, "diff.png"), fullPage: true });
  await row(identity).locator(".path").focus();
  await page.keyboard.press("Enter");
  await expect(editor(identity)).toBeVisible();
  await expect(editor(identity)).toBeFocused();
  await page.keyboard.press("ControlOrMeta+Shift+d");
  await expect(page.locator("#diff")).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator(".row.folded")).toHaveCount(0);
  await expect(page.locator(".cm-added")).toHaveCount(0);
  await expectOnlyMountedViews();
  commit("C9 diff");

  // A changed scenario or definition asks for the page again and keeps unsaved text.
  await typeAtStart(capture, "Kept. ");
  const scenarioFile = path.join(root, "scenarios/crew-v2-with-handoff.json");
  const scenario = JSON.parse(await fs.readFile(scenarioFile, "utf8"));
  scenario.values.display_name = "Renamed on disk";
  await fs.writeFile(scenarioFile, JSON.stringify(scenario, null, 2) + "\n");
  await expect(chip).toContainText("Renamed on disk");
  await expect(editor(capture)).toContainText("Kept. ");
  await page.keyboard.press("ControlOrMeta+s");
  await expect(saveState).toHaveText("All saved");
  commit("Renamed scenario");

  // A conflict that only the save reports takes the disk-change path: the local text stays unsaved and the disk keeps its text.
  await page.route("**/api/events", (route) => route.abort());
  await page.goto("about:blank");
  await page.goto(situationLink);
  await expect(page.locator("body")).not.toHaveAttribute("data-watching", "true");
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
  await page.unroute("**/api/events");

  // A save that fails midway keeps the files written before the failure saved.
  await page.goto("about:blank");
  await page.goto(situationLink);
  await expect(saveState).toHaveText("All saved");
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
  await page.goto(`${url}/#scenario=crew-v2-new-session`);
  await expect(page.locator(".event h2")).toHaveText(["crew/new-session"]);
  await page.goto(show("session/garden-ready"));
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
