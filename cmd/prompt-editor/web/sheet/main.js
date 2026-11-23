import { EditorView } from "@codemirror/view";
import { Files, sliceOf } from "./files.js";
import { el } from "./dom.js";
import { renderFile, renderPage } from "./render.js";
import { palette } from "./palette.js";

const files = new Files();
const $ = (id) => document.getElementById(id);
const documentRoot = $("document");
const kinds = ["situation", "scenario", "event", "file"];
let target = null;
let flips = {};
let rows = [];
let catalog = { situations: {}, scenarios: {}, recipients: [], sources: [] };
let diff = false;
let failed = false;

async function api(path, body) {
  const response = await fetch(`/api/${path}`, body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Prompt-Editor": "1" },
    body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw Object.assign(new Error(data.error ?? `${path} answered ${response.status}`), { status: response.status });
  return data;
}

function notice(text, kind = "info") {
  $("notice").textContent = text;
  $("notice").dataset.kind = kind;
}

function targetFromHash() {
  const params = new URLSearchParams(location.hash.slice(1));
  for (const kind of kinds) if (params.get(kind)) return { [kind]: params.get(kind) };
  return null;
}

const descriptions = {
  situation: (id) => catalog.situations[id]?.description,
  scenario: (id) => catalog.scenarios[id]?.description,
  event: () => null,
  file: () => "The file's raw text. Ctrl S saves it after the catalog validates it.",
};

function titleFor(events) {
  const [kind, id] = Object.entries(target)[0];
  $("title-kind").textContent = kind;
  $("title-id").textContent = id;
  $("title-count").textContent = events === undefined ? "" : `${events} event${events === 1 ? "" : "s"}`;
  $("description").textContent = descriptions[kind](id) ?? "";
}

function draw(drawn, scroll) {
  rows = drawn.rows;
  documentRoot.replaceChildren(...drawn.sections);
  documentRoot.setAttribute("aria-busy", "false");
  marks();
  window.scrollTo(0, scroll);
}

function showEmpty(...content) {
  rows = [];
  files.destroyFragments();
  documentRoot.replaceChildren(el("p", { class: "empty" }, ...content));
  documentRoot.setAttribute("aria-busy", "false");
}

async function loadFile(path) {
  const { sources, stale } = await api("catalog");
  if (!sources[path]) throw new Error(`${path} is not a registered source`);
  const scroll = window.scrollY;
  files.take({ [path]: sources[path] });
  files.destroyFragments();
  showStale(stale);
  $("base-commit").textContent = "";
  draw(renderFile(path, { files, navigation }), scroll);
  titleFor();
}

async function load() {
  if (target.file) return loadFile(target.file);
  const page = await api("page", { ...target, flips, ...(diff ? { base: $("base").value } : {}) });
  showStale(page.stale);
  const scroll = window.scrollY;
  files.take(page.sources);
  files.destroyFragments();
  $("base-commit").textContent = page.base_commit ? page.base_commit.slice(0, 10) : "";
  draw(renderPage(page, { files, flips, flip, navigation, expanded: marks, base: diff ? page.base ?? {} : undefined }), scroll);
  titleFor(page.events.length);
}

function showStale(stale) {
  $("stale").hidden = !stale;
}

async function refreshDefinitions() {
  $("refresh").disabled = true;
  notice("Regenerating the catalog from the Go definitions…");
  try {
    const refreshed = await api("operation", { op: "refresh" });
    catalog.recipients = refreshed.recipients;
    catalog.sources = Object.keys(refreshed.sources).sort();
    notice("Refreshed the Go definitions.");
    if (failed) await openTarget();
    else if (target) await load();
    else showStale(refreshed.stale);
  } catch (error) {
    notice(`Refresh failed: ${error.message}`, "error");
  } finally {
    $("refresh").disabled = false;
  }
}

async function openTarget() {
  target = targetFromHash();
  flips = {};
  if (!target) {
    showEmpty("Press Ctrl K to open a situation, a scenario, an event or a source file.");
    picker.show();
    return;
  }
  documentRoot.setAttribute("aria-busy", "true");
  window.scrollTo(0, 0);
  try {
    await load();
    failed = false;
    notice("");
  } catch (error) {
    failed = true;
    showStale((await api("catalog").catch(() => ({}))).stale);
    showEmpty(`This page did not open: ${error.message}`, el("br"), "If a source file caused this, Ctrl K opens it as raw text to fix and save.");
    titleFor(target.file ? undefined : 0);
    notice(error.message, "error");
  }
}

async function flip(name, value) {
  if (name in flips) delete flips[name];
  else flips[name] = String(value !== "true");
  try {
    await load();
    const active = Object.entries(flips).map(([field, flipped]) => `${field} = ${flipped}`).join(", ");
    notice(active ? `Flipped for this page only: ${active}. Nothing is written.` : "");
  } catch (error) {
    notice(error.message, "error");
  }
}

function marks() {
  for (const row of rows) {
    row.unsaved.hidden = !files.dirty(row.path);
    const conflict = files.conflict(row.path);
    row.conflict.hidden = !conflict;
    row.disk.textContent = conflict ? sliceOf(conflict.text, row.node) : "";
    const count = files.fragmentsOf(row.path);
    row.twice.hidden = count < 2;
    row.twice.textContent = `×${count} on this page`;
  }
  const dirty = files.dirtyPaths();
  const conflicts = dirty.filter((path) => files.conflict(path));
  $("save-state").textContent = dirty.length
    ? `● ${dirty.length} unsaved file${dirty.length === 1 ? "" : "s"}${conflicts.length ? `, ${conflicts.length} in conflict` : ""} · Ctrl S`
    : "All saved";
  $("save-state").dataset.dirty = String(dirty.length > 0);
  $("save-state").title = dirty.join("\n");
}

let saving = null;

function save() {
  saving ??= saveFiles().finally(() => { saving = null; });
  return saving;
}

async function saveFiles() {
  const request = files.saveRequest();
  if (!request.length) {
    notice(files.dirtyPaths().length ? "Only files in conflict are unsaved; Save skips them." : "Nothing to save.");
    return;
  }
  try {
    const answer = await api("save", { files: request });
    for (const file of request) if (answer.saved[file.path]) files.saved(file.path, file.text, answer.saved[file.path]);
    files.sync(answer.conflicts);
    const saved = Object.keys(answer.saved).length;
    const conflicts = Object.keys(answer.conflicts).filter((path) => files.conflict(path));
    const merged = Object.keys(answer.conflicts).filter((path) => !files.conflict(path) && files.dirty(path));
    const parts = [`Saved ${saved} file${saved === 1 ? "" : "s"}.`];
    if (merged.length) parts.push(`Merged changes on disk into ${merged.join(", ")}; save again to write them.`);
    if (conflicts.length) parts.push(`Changed on disk where you changed it, not saved: ${conflicts.join(", ")}.`);
    notice(parts.join(" "), conflicts.length ? "error" : "info");
  } catch (error) {
    notice(`Save failed: ${error.message}`, "error");
    if (error.status === 500) files.sync((await api("catalog")).sources);
  }
}

async function toggleDiff() {
  diff = !diff;
  $("diff").setAttribute("aria-pressed", String(diff));
  if (!target) return;
  try {
    await load();
    notice(diff ? `Showing changes since ${$("base").selectedOptions[0]?.textContent}. Unchanged fragments are folded; click a file name to open one.` : "");
  } catch (error) {
    diff = false;
    $("diff").setAttribute("aria-pressed", "false");
    notice(error.message, "error");
  }
}

let syncing = null;
let again = { sources: false, page: false };

function diskChanged(path) {
  if (path.endsWith(".md")) again.sources = true;
  else again.page = true;
  syncing ??= drainDisk().finally(() => { syncing = null; });
}

async function drainDisk() {
  while (again.sources || again.page) {
    await saving;
    const wanted = again;
    again = { sources: false, page: false };
    try {
      if (failed && target) await openTarget();
      else if (wanted.page && target) await load();
      else files.sync((await api("catalog")).sources);
    } catch (error) {
      notice(`Reading a change on disk failed: ${error.message}`, "error");
    }
  }
}

function watchDisk() {
  const events = new EventSource("/api/events");
  events.addEventListener("ready", () => { document.body.dataset.watching = "true"; });
  events.onerror = () => { document.body.dataset.watching = "false"; };
  events.onmessage = (message) => diskChanged(JSON.parse(message.data).path);
}

async function chooseBase() {
  const { refs, default: chosen } = await api("refs");
  if (chosen && !refs.includes(chosen)) refs.unshift(chosen);
  const label = (ref) => ref.replace(/^refs\/(heads|remotes|tags)\//, "");
  $("base").replaceChildren(...refs.map((ref) => el("option", { value: ref, selected: ref === chosen }, label(ref))));
  $("base").addEventListener("change", () => { if (diff) load().catch((error) => notice(error.message, "error")); });
}

function stops() {
  return [...documentRoot.querySelectorAll("[data-stop]")].filter((stop) => stop.offsetParent !== null && !stop.disabled);
}

function focusStop(stop, atEnd) {
  const view = stop.classList.contains("cm-content") ? EditorView.findFromDOM(stop) : null;
  if (!view) {
    stop.focus();
    stop.scrollIntoView({ block: "nearest" });
    return;
  }
  view.focus();
  view.dispatch({ selection: { anchor: atEnd ? view.state.doc.length : 0 }, scrollIntoView: true });
}

function move(from, step) {
  const all = stops();
  const next = all[all.indexOf(from) + step];
  if (!next) return false;
  focusStop(next, step < 0);
  return true;
}

function atEdge(view, forward) {
  const selection = view.state.selection.main;
  if (!selection.empty) return false;
  const end = forward ? view.state.doc.length : 0;
  if (selection.head === end) return true;
  const here = view.coordsAtPos(selection.head);
  const there = view.coordsAtPos(end);
  return Boolean(here && there && Math.abs(here.top - there.top) < 2);
}

const navigation = [
  { key: "ArrowDown", run: (view) => atEdge(view, true) && move(view.contentDOM, 1) },
  { key: "ArrowUp", run: (view) => atEdge(view, false) && move(view.contentDOM, -1) },
  { key: "ArrowRight", run: (view) => view.state.selection.main.empty && view.state.selection.main.head === view.state.doc.length && move(view.contentDOM, 1) },
  { key: "ArrowLeft", run: (view) => view.state.selection.main.empty && view.state.selection.main.head === 0 && move(view.contentDOM, -1) },
];

function paletteItems() {
  const items = [];
  for (const situation of Object.values(catalog.situations)) items.push({ kind: "situation", id: situation.id, description: situation.description });
  for (const scenario of Object.values(catalog.scenarios)) items.push({ kind: "scenario", id: scenario.id, description: scenario.description });
  for (const recipient of catalog.recipients)
    for (const event of recipient.events) items.push({ kind: "event", id: `${recipient.id}/${event.id}`, description: event.description });
  for (const path of catalog.sources) items.push({ kind: "file", id: path, description: "raw text" });
  for (const item of items) item.search = `${item.kind} ${item.id} ${item.description ?? ""}`.toLowerCase();
  return items;
}

const picker = palette({ items: paletteItems, open: (item) => { location.hash = `${item.kind}=${item.id}`; } });

function onKey(event) {
  const mod = event.ctrlKey || event.metaKey;
  if (mod && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "s") {
    event.preventDefault();
    event.stopPropagation();
    save();
  } else if (mod && event.shiftKey && !event.altKey && event.key.toLowerCase() === "d") {
    event.preventDefault();
    event.stopPropagation();
    toggleDiff();
  } else if (mod && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "k") {
    event.preventDefault();
    event.stopPropagation();
    picker.show();
  } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    const stop = event.target.closest?.("[data-stop]");
    if (!stop || !documentRoot.contains(stop)) return;
    if (!event.altKey && stop.classList.contains("cm-content")) return;
    if (move(stop, event.key === "ArrowDown" ? 1 : -1)) {
      event.preventDefault();
      event.stopPropagation();
    }
  }
}

async function start() {
  $("missing").remove();
  document.body.append(picker.element);
  document.addEventListener("keydown", onKey, true);
  $("open").addEventListener("click", () => picker.show());
  $("diff").addEventListener("click", toggleDiff);
  $("refresh").addEventListener("click", refreshDefinitions);
  window.addEventListener("hashchange", openTarget);
  window.addEventListener("beforeunload", (event) => {
    if (files.dirtyPaths().length) event.preventDefault();
  });
  files.listen(marks);
  const [situations, scenarios, fresh] = await Promise.all([
    api("operation", { op: "situations" }),
    api("operation", { op: "scenarios" }),
    api("catalog"),
  ]);
  catalog = { situations, scenarios, recipients: fresh.recipients, sources: Object.keys(fresh.sources).sort() };
  await chooseBase().catch((error) => notice(`Diff is unavailable: ${error.message}`, "error"));
  watchDisk();
  await openTarget();
}

start().catch((error) => notice(error.message, "error"));
