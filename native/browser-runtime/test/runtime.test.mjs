import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { afterEach, beforeEach, describe, it } from "node:test";
import { Window } from "happy-dom";

const ELEMENT_KEY = "element-6066-11e4-a52e-4f735466cecf";
const SHADOW_KEY = "shadow-6066-11e4-a52e-4f735466cecf";
const RUNTIME = readFileSync(new URL("../../crates/attn-app/src/browser/runtime.js", import.meta.url), "utf8");

let window;
let document;

function runtime() {
  const value = window.__attnBrowser;
  if (!value) throw new Error("browser runtime was not installed");
  return value;
}

function counter(target, type) {
  const calls = [];
  target.addEventListener(type, (event) => calls.push(event));
  return calls;
}

function replace(object, name, implementation) {
  const calls = [];
  object[name] = (...args) => {
    calls.push(args);
    return implementation?.(...args);
  };
  return calls;
}

function keyPress(value) {
  return {
    actions: [{
      type: "key",
      id: "keyboard",
      actions: [
        { type: "keyDown", value },
        { type: "keyUp", value },
      ],
    }],
  };
}

function pointerClick(x, y) {
  return {
    actions: [{
      type: "pointer",
      id: "mouse",
      actions: [
        { type: "pointerMove", x, y },
        { type: "pointerDown", button: 0 },
        { type: "pointerUp", button: 0 },
      ],
    }],
  };
}

describe("browser runtime", () => {
  beforeEach(() => {
    window = new Window({
      settings: {
        enableJavaScriptEvaluation: true,
        enableIframePageLoading: true,
        suppressInsecureJavaScriptEnvironmentWarning: true,
      },
    });
    document = window.document;
    window.eval(RUNTIME);
  });

  afterEach(async () => {
    await window.happyDOM.close();
  });

  it("finds semantic targets and operates them through stable references", async () => {
    document.body.innerHTML = `
      <label>Search <input /></label>
      <button>Submit</button>
    `;

    const input = await runtime().execute("find_element", { using: "label", value: "Search" });
    const button = await runtime().execute("find_element", { using: "role", value: "button", name: "Submit" });

    await runtime().execute("send_keys_to_element", { element: input[ELEMENT_KEY], text: "attn" });
    await runtime().execute("click_element", { element: button[ELEMENT_KEY] });

    assert.equal(document.querySelector("input").value, "attn");
    assert.match(input[ELEMENT_KEY], /^attn-element-/);
  });

  it("redacts passwords from snapshots without truncating typed input", async () => {
    document.body.innerHTML = `<input type="password" value="secret" />`;
    const input = document.querySelector("input");
    input.setSelectionRange(input.value.length, input.value.length);
    const inputRef = await runtime().execute("find_element", { using: "css", value: "input" });

    const snapshot = await runtime().execute("snapshot", {});
    await runtime().execute("send_keys_to_element", { element: inputRef[ELEMENT_KEY], text: "-more" });

    assert.equal(snapshot.elements[0]?.name, "");
    assert.equal(input.value, "secret-more");
  });

  it("finds elements inside shadow roots", async () => {
    const host = document.createElement("div");
    host.attachShadow({ mode: "open" }).innerHTML = "<button>Inside</button>";
    document.body.append(host);
    const hostRef = await runtime().execute("find_element", { using: "css", value: "div" });
    const shadowRef = await runtime().execute("get_element_shadow_root", { element: hostRef[ELEMENT_KEY] });
    const buttonRef = await runtime().execute("find_element_from_shadow", {
      shadow: shadowRef[SHADOW_KEY], using: "text", value: "Inside",
    });

    assert.match(shadowRef[SHADOW_KEY], /^attn-shadow-/);
    assert.match(buttonRef[ELEMENT_KEY], /^attn-element-/);
  });

  it("edits controls in a same-origin frame", async () => {
    const frame = document.createElement("iframe");
    document.body.append(frame);
    const frameDocument = frame.contentDocument;
    if (!frameDocument) throw new Error("frame document unavailable");
    frameDocument.body.innerHTML = "<label>Frame field <input></label><button>Frame button</button>";
    const frameRef = await runtime().execute("find_element", { using: "css", value: "iframe" });
    await runtime().execute("switch_to_frame", { id: frameRef });
    const inputRef = await runtime().execute("find_element", { using: "label", value: "Frame field" });
    await runtime().execute("send_keys_to_element", { element: inputRef[ELEMENT_KEY], text: "inside" });
    const input = frameDocument.querySelector("input");
    input.focus();
    await runtime().execute("perform_actions", keyPress("x"));

    const button = frameDocument.querySelector("button");
    const clicks = counter(button, "click");
    replace(frameDocument, "elementFromPoint", () => button);
    await runtime().execute("perform_actions", pointerClick(10, 10));

    assert.equal(input.value, "insidex");
    assert.equal(clicks.length, 1);
    assert.equal(frameDocument.activeElement, button);
    const scriptValue = await runtime().execute("execute_script", {
      script: "return document.querySelector('input').value;",
      args: [],
    });
    assert.equal(scriptValue, "insidex");
    await runtime().execute("switch_to_parent_frame", {});
  });

  it("keeps history navigation on the top-level page after switching frames", async () => {
    const frame = document.createElement("iframe");
    document.body.append(frame);
    const frameRef = await runtime().execute("find_element", { using: "css", value: "iframe" });
    await runtime().execute("switch_to_frame", { id: frameRef });
    const topBack = replace(window.history, "back");
    const topForward = replace(window.history, "forward");
    const frameBack = replace(frame.contentWindow.history, "back");
    const frameForward = replace(frame.contentWindow.history, "forward");

    await runtime().execute("back", {});
    await runtime().execute("forward", {});

    assert.equal(topBack.length, 1);
    assert.equal(topForward.length, 1);
    assert.equal(frameBack.length, 0);
    assert.equal(frameForward.length, 0);
  });

  it("applies Enter default behavior to the focused form control", async () => {
    document.body.innerHTML = `<form><input name="query" /></form>`;
    const input = document.querySelector("input");
    const submits = counter(document.querySelector("form"), "submit");
    document.querySelector("form").addEventListener("submit", (event) => event.preventDefault());
    input.focus();

    await runtime().execute("perform_actions", keyPress("Enter"));

    assert.equal(submits.length, 1);
  });

  it("keeps pointer coordinates across action ticks", async () => {
    document.body.innerHTML = `<button>Target</button>`;
    const button = document.querySelector("button");
    const clicks = counter(button, "click");
    replace(document, "elementFromPoint", (x, y) => (x === 40 && y === 30 ? button : null));

    await runtime().execute("perform_actions", pointerClick(40, 30));

    assert.equal(clicks.length, 1);
    assert.equal(document.activeElement, button);
  });

  it("focuses clicked controls before subsequent keyboard actions", async () => {
    document.body.innerHTML = `<input value="" />`;
    const input = document.querySelector("input");
    const inputRef = await runtime().execute("find_element", { using: "css", value: "input" });

    await runtime().execute("click_element", { element: inputRef[ELEMENT_KEY] });
    await runtime().execute("perform_actions", keyPress("x"));

    assert.equal(document.activeElement, input);
    assert.equal(input.value, "x");
  });

  it("does not insert printable keys while a command modifier is held", async () => {
    document.body.innerHTML = `<input value="seed" />`;
    const input = document.querySelector("input");
    input.focus();

    await runtime().execute("perform_actions", {
      actions: [{
        type: "key",
        id: "keyboard",
        actions: [
          { type: "keyDown", value: "Control" },
          { type: "keyDown", value: "a" },
          { type: "keyUp", value: "a" },
          { type: "keyUp", value: "Control" },
        ],
      }],
    });

    assert.equal(input.value, "seed");
  });

  it("starts input sources in the same action tick concurrently", async () => {
    document.body.innerHTML = `<input value="" />`;
    const input = document.querySelector("input");
    input.focus();
    const timers = replace(window, "setTimeout");

    const actions = runtime().execute("perform_actions", {
      actions: [
        { type: "none", id: "delay", actions: [{ type: "pause", duration: 1_000 }] },
        { type: "key", id: "keyboard", actions: [{ type: "keyDown", value: "x" }] },
      ],
    });

    assert.equal(input.value, "x");
    assert.deepEqual(timers.map(([, duration]) => duration), [1_000]);
    timers[0][0]();
    await actions;
  });
});
