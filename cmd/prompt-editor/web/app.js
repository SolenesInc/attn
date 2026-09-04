import { mergedRecipients, findEvent, sourceChange, renderDiff } from "./compare.js";

import { collaborate } from "./collaboration.js";

let collaboration;
const $ = (id) => document.getElementById(id);
const state = {
  catalog: null,
  key: "",
  path: "",
  drafts: new Map(),
  scenarios: new Map(),
  errors: new Map(),
  result: null,
  previewVersion: 0,
  saving: false,
  base: null,
  comparison: null,
  baseVersion: 0,
  sourceView: "edit",
  promptView: "current",
};
let previewTimer;
let previewAbort;
const bytes = (text) => new TextEncoder().encode(text).length;
function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}
function button(text, action, className) {
  const node = element("button", text, className);
  node.type = "button";
  node.addEventListener("click", action);
  return node;
}
async function api(path, body) {
  const response = await fetch(
    `/api/${path}`,
    body === undefined
      ? {}
      : {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Prompt-Editor": "1",
          },
          body: JSON.stringify(body),
        },
  );
  const data = await response.json();
  if (!response.ok)
    throw Object.assign(new Error(data.error || `Request failed (${response.status})`), data);
  return data;
}
function current() {
  const [recipient, event] = state.key.split("/");
  const r = recipients().find(
    (candidate) => candidate.id === recipient,
  );
  return {
    recipient: r,
    event: r.events.find((candidate) => candidate.id === event),
  };
}
function recipients() {
  return mergedRecipients(state.catalog.recipients, state.base?.recipients);
}
function fieldsFor(key) {
  const fields = new Map();
  for (const field of [...(state.catalog.fields[key] || []), ...(state.base?.fields[key] || [])])
    if (!fields.has(field.name)) fields.set(field.name, field);
  return [...fields.values()];
}
function changeFor(path) {
  return sourceChange(state.catalog, state.base, path, textFor(path));
}
function eventChange(key, visiting = new Set()) {
  if (!state.base || visiting.has(key)) return "";
  visiting.add(key);
  const before = findEvent(state.base, key);
  const after = findEvent(state.catalog, key);
  if (state.base.unavailable)
    return after && sourcePaths(after.body).some((path) => changeFor(path)) ? "source changed" : "";
  if (!before) return "added";
  if (!after) return "removed";
  if (JSON.stringify(before) !== JSON.stringify(after)) return "definition changed";
  if (sourcePaths(after.body).some((path) => changeFor(path))) return "source changed";
  if (fieldsFor(key).some((field) => field.from && eventChange(field.from, visiting))) return "input producer changed";
  return "";
}
function walk(node, visit) {
  visit(node);
  for (const binding of node.bindings || []) walk(binding.node, visit);
  for (const child of node.children || []) walk(child, visit);
}
function sourcePaths(node) {
  const paths = new Set();
  walk(node, (part) => {
    if (part.source) paths.add(part.source);
  });
  return [...paths];
}
function textFor(path) {
  return state.drafts.has(path)
    ? state.drafts.get(path)
    : state.catalog.sources[path]?.text ?? "";
}
function status() {
  const count = state.drafts.size;
  $("global-status").textContent = state.saving
    ? "Saving…"
    : count
      ? `${count} unsaved ${count === 1 ? "file" : "files"}`
      : "Checkout sources";
  const editable = Object.hasOwn(state.catalog.sources, state.path);
  $("file-state").textContent = state.drafts.has(state.path)
    ? "Unsaved draft"
    : editable ? "Saved on disk" : state.path ? "Removed from the current catalog" : "";
  $("source-size").textContent =
    `${bytes(textFor(state.path)).toLocaleString()} bytes`;
  $("save").disabled = !state.drafts.has(state.path) || state.saving;
  $("reset").disabled = !state.drafts.has(state.path) || state.saving;
  $("reload").disabled = !editable || state.saving;
  const error = state.errors.get(state.path);
  $("file-error").hidden = !error;
  $("file-error").textContent = error || "";
  collaboration?.contextLabel();
}
function renderNavigation() {
  $("changed-only").disabled = !state.base;
  const query = $("search").value.toLowerCase().trim();
  $("recipients").replaceChildren();
  for (const recipient of recipients()) {
    const events = recipient.events.filter((event) =>
      (!state.base || !$('changed-only').checked || eventChange(`${recipient.id}/${event.id}`)) &&
      `${recipient.id} ${event.id} ${sourcePaths(event.body).join(" ")}`
        .toLowerCase()
        .includes(query),
    );
    if (!events.length) continue;
    const group = element("div", undefined, "recipient-group");
    group.append(element("div", recipient.id, "recipient-heading"));
    for (const event of events) {
      const key = `${recipient.id}/${event.id}`;
      const dirty = sourcePaths(event.body).some((path) =>
        state.drafts.has(path),
      );
      const node = button(
        `${event.id}${dirty ? " •" : ""}`,
        () => selectEvent(key),
        `event-link${state.key === key ? " active" : ""}`,
      );
      if (state.key === key) node.setAttribute("aria-current", "page");
      const change = eventChange(key);
      if (change) node.append(element("small", change, "change-badge"));
      if (
        event.delivery === "available_skill" ||
        event.delivery === "reference"
      )
        node.append(element("small", event.delivery.replaceAll("_", " ")));
      group.append(node);
    }
    $("recipients").append(group);
  }
  $("all-sources").replaceChildren();
  for (const path of [...new Set([...Object.keys(state.catalog.sources), ...Object.keys(state.base?.sources || {})])].sort()) {
    const change = changeFor(path);
    if (path.toLowerCase().includes(query) && (!state.base || !$('changed-only').checked || change)) {
      const node = button(path.replace("content/", "") + (state.drafts.has(path) ? " •" : ""), () => selectSource(path), "source-link");
      if (change) node.append(element("small", change, "change-badge"));
      $("all-sources").append(node);
    }
  }
  $("event-change").textContent = eventChange(state.key);
}
function selectEvent(key) {
  if (collaboration?.reviewEvent() && key !== collaboration.reviewEvent()) return;
  const [recipient, event] = key.split("/");
  if (
    !recipients().some(
      (r) => r.id === recipient && r.events.some((e) => e.id === event),
    )
  )
    return;
  if (state.key !== key) { $("saved-scenario").value = ""; }
  state.key = key;
  history.replaceState(null, "", `${location.pathname}${location.search}#${key}`);
  state.result = null;
  const selected = current();
  $("recipient-name").textContent = selected.recipient.id;
  $("event-name").textContent = selected.event.id;
  $("event-description").textContent = selected.event.description;
  $("delivery").textContent = selected.event.delivery.replaceAll("_", " ");
  $("preview-description").textContent = [
    "available_skill",
    "reference",
  ].includes(selected.event.delivery)
    ? "Available to load. This does not mean it was included in a session."
    : "Composed content for these inputs. Actual delivery remains with the runtime adapter.";
  if (!state.scenarios.has(key)) {
    const values = Object.fromEntries(
      fieldsFor(key).map((field) => [
        field.name,
        field.kind === "flag" ? "false" : "",
      ]),
    );
    if (key === "session/launch")
      Object.assign(values, {
        notebook_root: "/workspace/notebook",
        garden_available: "true",
      });
    state.scenarios.set(key, values);
  }
  const values = state.scenarios.get(key);
  for (const field of fieldsFor(key))
    if (!Object.hasOwn(values, field.name)) values[field.name] = field.kind === "flag" ? "false" : "";
  renderInputs();
  renderNavigation();
  renderTree();
  const paths = sourcePaths(selected.event.body);
  selectSource(paths.includes(state.path) ? state.path : paths[0]);
  schedulePreview();
}
function renderInputs() {
  const fields = fieldsFor(state.key);
  const values = state.scenarios.get(state.key);
  $("input-count").textContent = fields.length
    ? `(${fields.length})`
    : "(none)";
  $("inputs").replaceChildren();
  $("presets").replaceChildren();
  if (state.key === "session/launch") {
    for (const [name, sample] of [
      [
        "Chief",
        {
          notebook_root: "/workspace/notebook",
          self_report_pull_requests: "false",
          workflow_enabled: "false",
          garden_available: "true",
        },
      ],
      [
        "Workspace",
        {
          notebook_root: "",
          self_report_pull_requests: "false",
          workflow_enabled: "true",
          garden_available: "true",
        },
      ],
      [
        "Plain session",
        {
          notebook_root: "",
          self_report_pull_requests: "false",
          workflow_enabled: "false",
          garden_available: "false",
          crew_priming: "",
        },
      ],
    ])
      $("presets").append(
        button(name, () => {
          $("saved-scenario").value = "";
          Object.assign(values, sample);
          renderInputs();
          schedulePreview();
        }),
      );
  }
  for (const field of fields) {
    const label = element("label", undefined, "input-field");
    label.title = field.description;
    const multiline =
      /conversation|brief|handoff|environment|opening_message|crew_priming|errors/.test(
        field.name,
      ) && field.kind !== "flag";
    const input = element(multiline ? "textarea" : "input");
    input.setAttribute("aria-label", field.name);
    if (field.kind === "flag") {
      input.type = "checkbox";
      input.checked = values[field.name] === "true";
    } else {
      if (!multiline) input.type = "text";
      input.value = values[field.name];
      input.placeholder = field.description;
      input.spellcheck = false;
    }
    input.addEventListener("input", () => {
      values[field.name] =
        field.kind === "flag" ? String(input.checked) : input.value;
      $("saved-scenario").value = "";
      schedulePreview();
    });
    label.append(element("span", field.name), input);
    if (!(state.catalog.fields[state.key] || []).some((f) => f.name === field.name))
      label.append(element("small", "Base input"));
    else if (state.base?.fields[state.key]?.some((f) => f.name === field.name && f.kind !== field.kind))
      label.append(element("small", "Input type differs in base"));
    if (field.from)
      label.append(
        button(`Inspect ${field.from}`, () => selectEvent(field.from)),
      );
    $("inputs").append(label);
  }
}
function renderTree() {
  $("tree").replaceChildren();
  const render = (node, trace, depth, branch = "") => {
    const row = element(
      "div",
      undefined,
      `tree-row ${trace ? (trace.selected ? "included" : "skipped") : ""}`,
    );
    row.style.setProperty("--depth", `${Math.min(depth, 9) * 12}px`);
    row.append(element("span", undefined, "tree-state"));
    if (node.source)
      row.append(
        button(
          node.id,
          () => selectSource(node.source),
          state.path === node.source ? "selected" : "",
        ),
      );
    else {
      let label =
        node.kind === "input"
          ? `Input: ${node.field.name}`
          : node.kind === "compose"
            ? "Paragraphs"
            : node.kind === "join"
              ? `Join ${JSON.stringify(node.separator || "")}`
              : node.kind === "trim"
                ? "Trim surrounding whitespace"
                : `${node.kind === "choose" ? "Choose" : "When"} ${node.condition.field.name}`;
      row.append(element("span", `${branch}${label}`));
      if (node.field?.from)
        row.append(button(node.field.from, () => selectEvent(node.field.from)));
    }
    if (trace?.reason) row.title = trace.reason;
    if (trace && ["when", "choose"].includes(node.kind))
      row.append(element("span", trace.reason, "reason"));
    $("tree").append(row);
    let index = 0;
    for (const binding of node.bindings || [])
      render(binding.node, trace?.children?.[index++], depth + 1);
    for (const [i, child] of (node.children || []).entries())
      render(
        child,
        trace?.children?.[index++],
        depth + 1,
        node.kind === "choose" ? (i === 0 ? "Yes: " : "Otherwise: ") : "",
      );
  };
  const removed = !findEvent(state.catalog, state.key);
  render(current().event.body, removed ? state.comparison?.base.result?.trace : state.result?.trace, 0);
}
function selectSource(path) {
  state.path = path || "";
  $("source").disabled = !Object.hasOwn(state.catalog.sources, path || "");
  $("filename").textContent = path
    ? path.replace("content/", "")
    : "This event has no Markdown source";
  $("source").value = path ? state.catalog.sources[path] ? textFor(path) : state.base?.sources[path]?.text || "" : "";
  if (path && !state.catalog.sources[path]) state.sourceView = "diff";
  $("uses").replaceChildren();
  if (path) {
    $("uses").append(element("span", "Used by "));
    for (const recipient of recipients())
      for (const event of recipient.events)
        if (sourcePaths(event.body).includes(path))
          $("uses").append(
            button(`${recipient.id}/${event.id}`, () =>
              selectEvent(`${recipient.id}/${event.id}`),
            ),
          );
  }
  status();
  renderTree();
  schedulePreview();
}
function renderViews() {
  const sourceDiff = state.sourceView === "diff";
  const promptDiff = state.promptView === "diff";
  $("source").hidden = sourceDiff;
  $("source-diff").hidden = !sourceDiff;
  $("output").hidden = promptDiff;
  $("prompt-diff").hidden = !promptDiff;
  $("source-edit").setAttribute("aria-pressed", String(!sourceDiff));
  $("source-compare").setAttribute("aria-pressed", String(sourceDiff));
  $("prompt-current").setAttribute("aria-pressed", String(!promptDiff));
  $("prompt-compare").setAttribute("aria-pressed", String(promptDiff));
  const comparison = state.comparison;
  const label = state.base ? `${state.base.ref.replace(/^refs\/(heads|remotes)\//, "")} · ${state.base.commit.slice(0, 8)}` : "Base";
  const currentLabel = collaboration?.reviewEvent() ? "Review snapshot" : "Working copy + drafts";
  const pending = state.base ? "Updating comparison…" : "Choose a base above to compare.";
  const change = state.path ? changeFor(state.path) : "";
  renderDiff($("source-diff"), comparison?.source_diff,
    !state.path ? "This event has no Markdown source." : !comparison ? pending :
      change === "added" ? "Added empty source." : change === "removed" ? "Removed empty source." : "No source changes.", label, currentLabel);
  let message = pending;
  if (comparison) {
    if (comparison.base.error) message = comparison.base.error;
    else if (comparison.current.error) message = `Current prompt: ${comparison.current.error}`;
    else if (comparison.base.status === "absent") message = "Event added in the working copy (empty for these inputs).";
    else if (comparison.current.status === "absent") message = "Event removed from the working copy (empty for these inputs).";
    else message = "No text changes for these inputs.";
  }
  renderDiff($("prompt-diff"), comparison?.prompt_diff, message, label, currentLabel);
  const before = comparison?.base.result?.delivery;
  const after = comparison?.current.result?.delivery;
  $("delivery").textContent = before && after && before !== after
    ? `${before} → ${after}`.replaceAll("_", " ")
    : current().event.delivery.replaceAll("_", " ");
}
function schedulePreview() {
  clearTimeout(previewTimer);
  previewAbort?.abort();
  const version = ++state.previewVersion;
  state.comparison = null;
  $("output-size").textContent = "Updating…";
  $("copy").disabled = true;
  renderViews();
  previewTimer = setTimeout(() => preview(version), 180);
}
async function preview(version) {
  previewAbort = new AbortController();
  try {
    const { recipient, event } = current();
    let values = state.scenarios.get(state.key);
    if (!state.base) {
      const fields = new Set((state.catalog.fields[state.key] || []).map((f) => f.name));
      values = Object.fromEntries(Object.entries(values).filter(([name]) => fields.has(name)));
    }
    const response = await fetch(state.base ? "/api/compare" : "/api/preview", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Prompt-Editor": "1" },
      signal: previewAbort.signal,
      body: JSON.stringify({
        recipient: recipient.id,
        event: event.id,
        values,
        drafts: Object.fromEntries(state.drafts),
        ...collaboration?.payload(),
        ...(state.base ? { base_commit: state.base.commit, path: state.path } : {}),
      }),
    });
    const data = await response.json();
    if (version !== state.previewVersion) return;
    if (!response.ok) throw new Error(data.error || "Preview failed");
    state.comparison = state.base ? data : null;
    state.result = state.base ? data.current.result || null : data;
    if (data.values && $("saved-scenario").value) { state.scenarios.set(state.key, data.values); renderInputs(); }
    const error = state.base ? data.current.error : "";
    $("preview-error").hidden = !error;
    $("preview-error").textContent = error || "";
    $("output").textContent = state.result ? state.result.text || "(No content for these inputs.)" : error ? "" : "Event removed from the current catalog.";
    $("output-size").textContent = state.result ? `${bytes(state.result.text).toLocaleString()} bytes` : error ? "Invalid draft" : "Removed event";
    $("copy").disabled = !state.result;
    renderTree();
    renderViews();
    collaboration?.contextLabel();
  } catch (error) {
    if (version !== state.previewVersion || error.name === "AbortError") return;
    state.result = null;
    state.comparison = { source_diff: "", prompt_diff: "", base: {}, current: { error: error.message } };
    $("preview-error").textContent = error.message;
    $("preview-error").hidden = false;
    $("output").textContent = "";
    $("output-size").textContent = "Preview unavailable";
    renderTree();
    renderViews();
    collaboration?.contextLabel();
    if (state.base) renderDiff($("source-diff"), "", error.message, state.base.commit.slice(0, 8));
  }
}
function refreshSelection() {
  const selectedPath = state.path;
  if (!recipients().some((r) => r.events.some((e) => `${r.id}/${e.id}` === state.key)))
    state.key = "session/launch";
  selectEvent(state.key);
  if (state.catalog.sources[selectedPath] || state.base?.sources[selectedPath]) selectSource(selectedPath);
}
async function selectBase() {
  const version = ++state.baseVersion;
  const selection = { key: state.key, path: state.path };
  const ref = $("base-ref").value.trim();
  const mode = $("base-mode").value;
  state.base = null;
  $("base-status").textContent = "Reading Git revision…";
  $("base-error").hidden = true;
  refreshSelection();
  const loadingSelection = { key: state.key, path: state.path };
  try {
    const base = await api("base", { ref, mode });
    if (version !== state.baseVersion) return;
    state.base = base;
    $("base-status").textContent = `${mode === "tip" ? "Branch tip" : "Merge base"} · ${base.commit.slice(0, 8)}${base.unavailable ? " · Source comparison only" : ""}`;
    $("base-status").title = `Base: ${base.commit}\nBranch tip: ${base.tip}\nHEAD: ${base.head}\nPinned until Compare / refresh is pressed.`;
    $("base-error").hidden = !base.unavailable;
    $("base-error").textContent = base.unavailable || "";
    if (state.key === loadingSelection.key && state.path === loadingSelection.path) {
      state.key = selection.key;
      state.path = selection.path;
    }
    refreshSelection();
  } catch (error) {
    if (version !== state.baseVersion) return;
    $("base-status").textContent = "Comparison unavailable";
    $("base-error").hidden = false;
    $("base-error").textContent = error.message;
    refreshSelection();
  }
}
async function loadRefs() {
  try {
    const data = await api("refs");
    for (const ref of data.refs || []) {
      const option = element("option");
      option.value = ref.replace(/^refs\/(heads|remotes)\//, "");
      option.label = ref;
      $("base-refs").append(option);
    }
    if (data.default) {
      $("base-ref").value = data.default.replace(/^refs\/(heads|remotes)\//, "");
      await selectBase();
    }
  } catch (error) {
    $("base-status").textContent = "Git comparison unavailable";
    $("base-error").hidden = false;
    $("base-error").textContent = error.message;
  }
}
async function save() {
  try { if (await collaboration?.save()) return; }
  catch (error) { state.errors.set(state.path, error.message); status(); return; }
  const path = state.path;
  if (!path || !state.drafts.has(path) || state.saving) return;
  const text = textFor(path);
  state.saving = true;
  state.errors.delete(path);
  status();
  try {
    const saved = await api("save", {
      path,
      text,
      revision: state.catalog.sources[path].revision,
    });
    state.catalog.sources[path] = saved;
    if (state.drafts.get(path) === text) state.drafts.delete(path);
    renderNavigation();
    schedulePreview();
  } catch (error) {
    state.errors.set(path, error.message);
  } finally {
    state.saving = false;
    status();
  }
}
$("source").addEventListener("input", () => {
  const path = state.path;
  if ($("source").value === state.catalog.sources[path].text)
    state.drafts.delete(path);
  else state.drafts.set(path, $("source").value);
  state.errors.delete(path);
  collaboration?.edited(path, $("source").value);
  status();
  renderNavigation();
  schedulePreview();
});
$("save").addEventListener("click", save);
$("reset").addEventListener("click", async () => {
  try { if (await collaboration?.reset()) return; }
  catch (error) { state.errors.set(state.path, error.message); status(); return; }
  state.drafts.delete(state.path);
  state.errors.delete(state.path);
  selectSource(state.path);
  renderNavigation();
  schedulePreview();
});
$("reload").addEventListener("click", async () => {
  try { if (await collaboration?.reload()) return; }
  catch (error) { state.errors.set(state.path, error.message); status(); return; }
  const path = state.path;
  if (
    state.drafts.has(path) &&
    !confirm("Discard this file’s draft and reload it from disk?")
  )
    return;
  try {
    const fresh = await api("catalog");
    state.catalog.sources[path] = fresh.sources[path];
    state.drafts.delete(path);
    state.errors.delete(path);
    if (state.path === path) selectSource(path);
    renderNavigation();
    schedulePreview();
  } catch (error) {
    state.errors.set(path, error.message);
    status();
  }
});
$("search").addEventListener("input", renderNavigation);
$("changed-only").addEventListener("change", renderNavigation);
$("base-form").addEventListener("submit", (event) => { event.preventDefault(); selectBase(); });
$("base-mode").addEventListener("change", selectBase);
$("clear-base").addEventListener("click", () => {
  ++state.baseVersion;
  state.base = null;
  $("base-error").hidden = true;
  $("base-status").textContent = "Choose a base for comparison.";
  $("base-status").title = "";
  $("changed-only").checked = false;
  refreshSelection();
});
for (const [id, key, value] of [
  ["source-edit", "sourceView", "edit"], ["source-compare", "sourceView", "diff"],
  ["prompt-current", "promptView", "current"], ["prompt-compare", "promptView", "diff"],
]) $(id).addEventListener("click", () => { state[key] = value; renderViews(); });
$("inputs").addEventListener("submit", (event) => event.preventDefault());
$("copy").addEventListener("click", async () => {
  if (!state.result) return;
  try {
    await navigator.clipboard.writeText(state.result.text);
    $("global-status").textContent = "Resolved text copied";
  } catch {
    $("global-status").textContent = "Select the resolved text to copy it";
  }
});
window.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
    event.preventDefault();
    save();
  }
});
window.addEventListener("beforeunload", (event) => {
  if (collaboration?.pending() || (!collaboration?.active() && state.drafts.size)) {
    event.preventDefault();
    event.returnValue = "";
  }
});
window.addEventListener("hashchange", () =>
  selectEvent(location.hash.slice(1)),
);
try {
  state.catalog = await api("catalog");
  const requestedKey = location.hash.slice(1) || "session/launch";
  selectEvent(requestedKey);
  if (!state.key) selectEvent("session/launch");
  await loadRefs();
  if (state.key !== requestedKey) selectEvent(requestedKey);
  collaboration = collaborate({ state, $, api, selectEvent, selectSource, renderNavigation, schedulePreview, status });
  await collaboration.init();
} catch (error) {
  $("global-status").textContent = "Could not open catalog";
  $("file-error").hidden = false;
  $("file-error").textContent = error.message;
}
