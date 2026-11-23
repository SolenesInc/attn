import { EditorView } from "@codemirror/view";
import { Files } from "./files.js";
import { el } from "./dom.js";
import { renderPage } from "./render.js";
import { palette } from "./palette.js";

const files = new Files();
const $ = (id) => document.getElementById(id);
const documentRoot = $("document");
const kinds = ["situation", "scenario", "event"];
let target = null;
let flips = {};
let rows = [];
let catalog = { situations: {}, scenarios: {}, recipients: [] };

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

function titleFor(page) {
  const [kind, id] = Object.entries(target)[0];
  const description = kind === "situation" ? catalog.situations[id]?.description : kind === "scenario" ? catalog.scenarios[id]?.description : null;
  $("title-kind").textContent = kind;
  $("title-id").textContent = id;
  $("title-count").textContent = `${page.events.length} event${page.events.length === 1 ? "" : "s"}`;
  $("description").textContent = description ?? "";
}

async function load() {
  const page = await api("page", { ...target, flips });
  files.take(page.sources);
  files.detachAll();
  const scroll = window.scrollY;
  const drawn = renderPage(page, { files, flips, flip, navigation, expanded: marks });
  rows = drawn.rows;
  documentRoot.replaceChildren(...drawn.sections);
  documentRoot.setAttribute("aria-busy", "false");
  titleFor(page);
  marks();
  window.scrollTo(0, scroll);
}

async function openTarget() {
  target = targetFromHash();
  flips = {};
  if (!target) {
    documentRoot.replaceChildren(el("p", { class: "empty" }, "Press Ctrl K to open a situation, a scenario or an event."));
    documentRoot.setAttribute("aria-busy", "false");
    picker.show();
    return;
  }
  documentRoot.setAttribute("aria-busy", "true");
  window.scrollTo(0, 0);
  try {
    await load();
    notice("");
  } catch (error) {
    rows = [];
    files.detachAll();
    documentRoot.replaceChildren(el("p", { class: "empty" }, `This page did not open: ${error.message}`));
    documentRoot.setAttribute("aria-busy", "false");
    titleFor({ events: [] });
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
    row.conflict.hidden = !files.conflict(row.path);
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

async function save() {
  const request = files.saveRequest();
  if (!request.length) {
    notice(files.dirtyPaths().length ? "Only files in conflict are unsaved; Save skips them." : "Nothing to save.");
    return;
  }
  try {
    const answer = await api("save", { files: request });
    for (const file of request) if (answer.saved[file.path]) files.saved(file.path, file.text, answer.saved[file.path]);
    for (const [path, disk] of Object.entries(answer.conflicts)) files.conflicted(path, disk);
    const saved = Object.keys(answer.saved).length;
    const conflicts = Object.keys(answer.conflicts);
    notice(conflicts.length
      ? `Saved ${saved} file${saved === 1 ? "" : "s"}. Changed on disk, not saved: ${conflicts.join(", ")}.`
      : `Saved ${saved} file${saved === 1 ? "" : "s"}.`, conflicts.length ? "error" : "info");
  } catch (error) {
    notice(`Save failed: ${error.message}`, "error");
    if (error.status === 500) files.reloadDisk((await api("catalog")).sources);
  }
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
  catalog = { situations, scenarios, recipients: fresh.recipients };
  await openTarget();
}

start().catch((error) => notice(error.message, "error"));
