import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const repo = fileURLToPath(new URL("../../..", import.meta.url));
const require = createRequire(path.join(repo, "app/package.json"));
const { chromium, expect } = require("@playwright/test");
const fixture = await fs.mkdtemp(path.join(os.tmpdir(), "prompt-editor-collaboration-"));
const artifacts = process.env.PROMPT_EDITOR_ARTIFACTS;
const root = path.join(fixture, "internal/prompts");
const source = "content/crew/wake.md";
const wake = path.join(root, source);
const git = (...args) => execFileSync("git", ["-C", fixture, ...args], { encoding: "utf8", env: { ...process.env, GIT_CONFIG_GLOBAL: os.devNull, GIT_CONFIG_NOSYSTEM: "1" } }).trim();
const binary = process.env.PROMPT_EDITOR_BIN || path.join(fixture, "prompt-editor");
const cli = (...args) => JSON.parse(execFileSync(binary, ["--repo", fixture, "--json", ...args], { encoding: "utf8" }));
const put = async (id, text) => {
    const info = cli("inspect", "crew/wake", "--draft", id);
    const file = path.join(fixture, "agent-edit.txt");
    await fs.writeFile(file, text);
    return cli("draft", "put", id, source, "--file", file, "--expect", info.sources[source].revision, "--author", "agent");
};
let server, browser, context, secondContext, watcher;
const errors = [];
async function start(port = 0) {
    server = spawn(binary, ["--repo", fixture, "--port", String(port), "--base", "next"], { stdio: ["ignore", "pipe", "pipe"] });
    return new Promise((resolve, reject) => {
        let output = "";
        const timer = setTimeout(() => reject(new Error("Editor did not start")), 15000);
        server.once("error", (error) => { clearTimeout(timer); reject(error); });
        server.once("exit", (code) => { clearTimeout(timer); reject(new Error(`Editor exited ${code}`)); });
        server.stdout.on("data", (chunk) => { output += chunk; const match = output.match(/http:\/\/127\.0\.0\.1:\d+/); if (match) {
            clearTimeout(timer);
            resolve(match[0]);
        } });
        server.stderr.on("data", (chunk) => process.stderr.write(chunk));
    });
}
try {
    await fs.mkdir(root, { recursive: true });
    for (const name of ["content", "scenarios"])
        await fs.cp(path.join(repo, "internal/prompts", name), path.join(root, name), { recursive: true });
    const manifest = JSON.parse(await fs.readFile(path.join(repo, "internal/prompts/catalog.generated.json"), "utf8"));
    delete manifest.definitions_hash;
    await fs.writeFile(path.join(root, "catalog.generated.json"), JSON.stringify(manifest));
    await fs.writeFile(wake, "Wake from the base.\n");
    git("init", "-b", "next");
    git("add", ".");
    git("-c", "user.name=Editor test", "-c", "user.email=editor@example.test", "commit", "--no-gpg-sign", "-m", "Prompt fixture");
    if (!process.env.PROMPT_EDITOR_BIN)
        execFileSync("go", ["build", "-o", binary, "./cmd/prompt-editor"], { cwd: repo, stdio: "inherit" });
    const url = await start();
    if (artifacts)
        await fs.mkdir(artifacts, { recursive: true });
    browser = await chromium.launch({ headless: true });
    context = await browser.newContext({ viewport: { width: 1512, height: 1050 }, permissions: ["clipboard-read", "clipboard-write"], ...(artifacts ? { recordVideo: { dir: artifacts, size: { width: 1512, height: 1050 } } } : {}) });
    const page = await context.newPage();
    page.on("pageerror", (error) => errors.push(String(error)));
    await page.goto(`${url}/#crew/wake`);
    await expect(page.locator("#source")).toHaveValue("Wake from the base.\n");
    await expect(page.locator("#saved-scenario option")).toHaveCount(8);
    await page.locator("#saved-scenario").selectOption("crew-wake");
    page.once("dialog", (dialog) => dialog.accept("Clear wake instructions"));
    await page.locator("#new-draft").click();
    await expect(page.locator("#draft-state")).toContainText("d-");
    const draftID = new URL(page.url()).searchParams.get("draft");
    assert.ok(draftID);
    await page.locator("#source").fill("Wake from the maintainer's shared draft.\n");
    await expect(page.locator("#file-state")).toContainText("Shared draft");
    let d = cli("draft", "get", draftID);
    assert.equal(d.files[source].text, "Wake from the maintainer's shared draft.\n");
    assert.equal(await fs.readFile(wake, "utf8"), "Wake from the base.\n");
    d = await put(draftID, "Wake with the agent's clarification.\n");
    await expect(page.locator("#source")).toHaveValue("Wake with the agent's clarification.\n");
    await expect(page.locator("#output")).toContainText("agent's clarification");
    const link = cli("show", "--draft", draftID, "--event", "crew/heartbeat");
    assert.equal(link.url, `${url}/?draft=${draftID}`);
    await expect(page.locator("#event-name")).toHaveText("wake");
    await page.locator("#follow-agent").check();
    cli("show", "--draft", draftID, "--event", "crew/priming", "--scenario", "crew-with-handoff");
    await expect(page.locator("#event-name")).toHaveText("priming");
    cli("show", "--draft", draftID, "--event", "crew/wake", "--source", source, "--scenario", "crew-wake");
    await expect(page.locator("#source")).toHaveValue("Wake with the agent's clarification.\n");
    await expect(page.locator("#event-name")).toHaveText("wake");
    await page.locator("#follow-agent").uncheck();
    secondContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const second = await secondContext.newPage();
    second.on("pageerror", (error) => errors.push(String(error)));
    await second.goto(link.url);
    await expect(second.locator("#source")).toHaveValue("Wake with the agent's clarification.\n");
    let release;
    let intercepted;
    const held = new Promise((resolve) => { release = resolve; });
    const arrived = new Promise((resolve) => { intercepted = resolve; });
    await second.route("**/api/operation", async (route) => {
        if (route.request().postDataJSON()?.op === "draft-put") {
            intercepted();
            await held;
        }
        await route.continue();
    });
    await second.locator("#source").fill("Keep this concurrent maintainer edit.\n");
    await arrived;
    d = await put(draftID, "Keep this concurrent agent edit.\n");
    release();
    await expect(second.locator("#collaboration-error")).toContainText("source changed");
    await expect(second.locator("#source")).toHaveValue("Keep this concurrent maintainer edit.\n");
    await expect(page.locator("#source")).toHaveValue("Keep this concurrent agent edit.\n");
    await second.locator("#fork-local").click();
    await expect(second.locator("#collaboration-error")).toBeHidden();
    await expect(second).not.toHaveURL(new RegExp(draftID));
    await expect(second.locator("#file-state")).toContainText("Shared draft");
    const forkID = new URL(second.url()).searchParams.get("draft");
    assert.notEqual(forkID, draftID);
    assert.equal(cli("draft", "get", forkID).files[source].text, "Keep this concurrent maintainer edit.\n");
    assert.equal(cli("draft", "get", draftID).files[source].text, "Keep this concurrent agent edit.\n");
    await page.locator("#share-review").click();
    await expect(page.locator("#draft-state")).toContainText("snapshot of revision");
    const reviewID = new URL(page.url()).searchParams.get("review");
    await page.locator("#source-edit").click();
    await page.locator("#prompt-current").click();
    await expect(page.locator("#source")).toHaveJSProperty("readOnly", true);
    await expect(page.locator("#output")).toContainText("Keep this concurrent agent edit.");
    if (artifacts)
        await page.screenshot({ path: path.join(artifacts, "shared-review.png") });
    await put(draftID, "A later agent edit outside the review.\n");
    await expect(page.locator("#output")).toContainText("Keep this concurrent agent edit.");
    watcher = spawn(binary, ["--repo", fixture, "--json", "watch", "--review", reviewID, "--after", "0", "--timeout", "10s"], { stdio: ["ignore", "pipe", "pipe"] });
    let watched = "";
    watcher.stdout.on("data", (data) => { watched += data; });
    const watchDone = once(watcher, "exit");
    await page.locator("#output").evaluate((node) => { const range = document.createRange(); range.selectNodeContents(node); const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range); node.dispatchEvent(new MouseEvent("mouseup", { bubbles: true })); });
    await page.locator("#discussion summary").click();
    await page.locator("#feedback-message").fill("Keep the instruction, but explain what to inspect first.");
    await page.locator("#send-feedback").click();
    await expect(page.locator("#feedback-list")).toContainText("explain what to inspect first");
    const [watchCode] = await watchDone;
    assert.equal(watchCode, 0);
    const feedback = JSON.parse(watched).feedback[0];
    assert.equal(feedback.target, "prompt");
    assert.equal(feedback.selection, "Keep this concurrent agent edit.");
    assert.equal(cli("inspect", "--review", reviewID).result.text, "Keep this concurrent agent edit.");
    await page.locator(".shared-tools summary").click();
    await page.locator("#copy-agent-context").click();
    await expect(page.locator("#draft-state")).toHaveText("Agent context copied");
    const clipboard = await page.evaluate(() => navigator.clipboard.readText());
    assert.ok(clipboard.includes(`inspect --review ${reviewID} --json`));
    if (artifacts)
        await page.screenshot({ path: path.join(artifacts, "review-feedback.png") });
    await page.locator("#return-draft").click();
    await expect(page.locator("#source")).toHaveJSProperty("readOnly", false);
    await expect(page.locator("#source")).toHaveValue("A later agent edit outside the review.\n");
    await page.locator("#source-edit").click();
    await fs.writeFile(wake, "External file edit.\n");
    await page.locator("#apply-draft").click();
    await expect(page.locator("#collaboration-error")).toContainText("file changed on disk");
    assert.equal(await fs.readFile(wake, "utf8"), "External file edit.\n");
    d = cli("draft", "get", draftID);
    cli("draft", "reset", draftID, source, "--expect", d.files[source].revision);
    d = await put(draftID, "Resolved with both collaborators.\n");
    await expect(page.locator("#source")).toHaveValue("Resolved with both collaborators.\n");
    await page.locator("#apply-draft").click();
    await expect(page.locator("#file-state")).toHaveText("Saved on disk");
    assert.equal(await fs.readFile(wake, "utf8"), "Resolved with both collaborators.\n");
    await page.locator("#archive-draft").click();
    await expect(page.locator("#draft-state")).toHaveText("Checkout files");
    await page.locator("#shared-draft").selectOption(draftID);
    await expect(page.locator("#source")).toHaveJSProperty("readOnly", true);
    await page.locator("#restore-draft").click();
    await expect(page.locator("#source")).toHaveJSProperty("readOnly", false);
    const reviewURL = cli("show", "--review", reviewID).url;
    await page.goto(reviewURL);
    await expect(page.locator("#source")).toHaveJSProperty("readOnly", true);
    await expect(page.locator("#source")).toHaveValue("Keep this concurrent agent edit.\n");
    await page.locator("#source-edit").click();
    await expect(page.locator("#source")).toBeVisible();
    await page.locator("#source").focus();
    await page.locator("#source").evaluate((node) => { node.setSelectionRange(5, 9); node.dispatchEvent(new Event("select")); });
    await expect(page.locator("#selection-context")).toHaveText("Selected source: this");
    await page.locator("#discussion summary").click();
    await page.locator("#feedback-message").fill("Source selection stays anchored to this review.");
    await page.locator("#send-feedback").click();
    await expect(page.locator("#feedback-count")).toHaveText("(2)");
    const sourceFeedback = cli("review", "get", reviewID).feedback[1];
    assert.equal(sourceFeedback.selection, "this");
    assert.equal(sourceFeedback.path, source);
    assert.ok(sourceFeedback.source_revision);
    if (artifacts) await page.screenshot({ path: path.join(artifacts, "source-feedback.png") });
    await secondContext.close();
    secondContext = null;
    server.kill("SIGTERM");
    await once(server, "exit");
    assert.throws(() => cli("show", "--review", reviewID), (error) => error.status === 2 && JSON.parse(error.stdout).error.includes("editor is no longer running"));
    cli("review", "comment", reviewID, "--message", "Feedback added while the editor was stopped.");
    await start(Number(new URL(url).port));
    await expect(page.locator("#feedback-count")).toHaveText("(3)", { timeout: 10000 });
    await page.goto(reviewURL);
    await page.locator("#prompt-current").click();
    await expect(page.locator("#output")).toContainText("Keep this concurrent agent edit.");
    await expect(page.locator("#feedback-list")).toContainText("explain what to inspect first");
    assert.equal(cli("draft", "get", forkID).files[source].text, "Keep this concurrent maintainer edit.\n");
    let requests = 0;
    page.on("request", () => requests++);
    const idleBefore = execFileSync("ps", ["-p", String(server.pid), "-o", "rss=", "-o", "time="], { encoding: "utf8" }).trim();
    await page.waitForTimeout(1200);
    const idleAfter = execFileSync("ps", ["-p", String(server.pid), "-o", "rss=", "-o", "time="], { encoding: "utf8" }).trim();
    assert.equal(requests, 0, "shared editor polls while idle");
    assert.deepEqual(errors, []);
    console.log(JSON.stringify({ status: "passed", draftID, forkID, reviewID, feedback, idleRequests: requests, idleBefore, idleAfter, artifacts }, null, 2));
}
finally {
    if (secondContext)
        await secondContext.close();
    if (context)
        await context.close();
    if (browser)
        await browser.close();
    for (const child of [watcher, server])
        if (child && child.exitCode === null) {
            child.kill("SIGTERM");
            await once(child, "exit");
        }
    await fs.rm(fixture, { recursive: true, force: true });
}
