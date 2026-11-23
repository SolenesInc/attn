import { el, short } from "./dom.js";
import { fragmentEditor, replaceText } from "./fragment.js";
import { sliceOf } from "./files.js";

export function renderPage(page, context) {
  const rows = [];
  const scope = { ...context, rows, sources: page.sources };
  const sections = page.events.map((event, index) => renderEvent(event, index, scope));
  return { sections, rows };
}

function renderEvent(event, index, scope) {
  const doc = el("div", { class: "doc" });
  walk(event.definition.body, event.trace, doc, eventScope(scope, event), 0, false);
  return el("section", { class: "event", "data-event": event.event },
    el("header", { class: "event-head" },
      el("span", { class: "event-index" }, String(index + 1)),
      el("h2", {}, event.event),
      el("span", { class: "delivery" }, event.definition.delivery),
      event.scenario ? el("span", { class: "scenario" }, event.scenario) : null),
    el("p", { class: "event-description" }, event.definition.description),
    doc);
}

function eventScope(scope, event) {
  return { ...scope, values: event.values ?? {}, producers: event.producers ?? {} };
}

function walk(node, trace, into, scope, depth, untaken) {
  const children = node.children ?? [];
  if (node.kind === "text") return textRow(node, trace, into, scope, depth, untaken);
  if (node.kind === "input") return inputRow(node, trace, into, scope, depth);
  if (node.kind === "when" || node.kind === "choose") {
    children.forEach((child, index) => {
      const childTrace = trace?.children?.[index];
      if (!untaken && childTrace?.selected) walk(child, childTrace, into, scope, depth, false);
      else into.append(stub(node, index, child, childTrace, trace, scope, depth));
    });
    return;
  }
  children.forEach((child, index) => walk(child, trace?.children?.[index], into, scope, depth, untaken));
}

function producerOf(node, scope) {
  return node.kind === "input" && node.field.from ? scope.producers[node.field.name] : undefined;
}

function textRow(node, trace, into, scope, depth, untaken) {
  const { files } = scope;
  const path = node.source;
  const bound = {};
  const nested = [];
  (node.bindings ?? []).forEach((binding, index) => {
    const childTrace = trace?.children?.[index];
    const producer = producerOf(binding.node, scope);
    if (binding.node.kind === "input" && !producer) {
      bound[binding.name] = { value: childTrace?.selected ? childTrace.text ?? "" : scope.values[binding.name] };
      return;
    }
    bound[binding.name] = { nested: true };
    nested.push({ binding, childTrace, producer });
  });
  const unsaved = el("span", { class: "unsaved", hidden: true }, "● unsaved");
  const twice = el("span", { class: "tag twice", hidden: true, title: "This file is in more than one place on this page. Every place follows your typing." });
  const disk = el("pre", { class: "conflict-disk" });
  const conflict = el("div", { class: "conflict", hidden: true, role: "alert" },
    el("p", {}, "This file changed on disk where you changed it too. Save skips this file until you choose. On disk now:"),
    disk,
    el("div", { class: "conflict-actions" },
      el("button", { type: "button", onclick: () => scope.files.keepMine(path) }, "Keep mine"),
      el("button", { type: "button", onclick: () => scope.files.useDisk(path) }, "Use disk")));
  const body = el("div", { class: "body" }, conflict);
  body.style.setProperty("--depth", String(depth));
  const text = sliceOf(files.text(path), node);
  const base = scope.base ? sliceOf(scope.base[path] ?? "", node) : undefined;
  const folded = base === text;
  const label = el("button", { class: "path", type: "button", title: path, tabindex: "-1", "data-stop": folded ? "" : null, onclick: () => { unfold(); view.focus(); } }, short(path));
  const row = el("div", { class: ["row", untaken && "untaken", folded && "folded"].filter(Boolean).join(" "), "data-path": path },
    el("div", { class: "gutter" }, label, unsaved, twice, usesTag(scope.sources[path])),
    body);
  into.append(row);
  const view = fragmentEditor({
    parent: body,
    text,
    base,
    chip: node.verbatim ? null : (name) => bound[name] ?? { value: scope.values[name] },
    onChange: (text) => files.edit(path, node, text, view),
    navigation: scope.navigation,
    label: short(path),
  });
  const unfold = () => {
    row.classList.remove("folded");
    label.removeAttribute("data-stop");
  };
  files.attach(path, node, view, (text) => {
    replaceText(view, text);
    if (text !== base) unfold();
  });
  scope.rows.push({ path, node, unsaved, twice, conflict, disk });
  for (const { binding, childTrace, producer } of nested) {
    const block = nestedBlock(binding.name, producer ? `rendered by ${producer.event}` : `fills ${short(path)}`, into, depth);
    if (producer) walk(producer.definition.body, producer.trace, block, eventScope(scope, producer), depth + 1, untaken);
    else walk(binding.node, childTrace, block, scope, depth + 1, untaken);
  }
}

function inputRow(node, trace, into, scope, depth) {
  const name = node.field.name;
  const producer = producerOf(node, scope);
  if (producer) {
    const block = nestedBlock(name, `rendered by ${producer.event} · ${producer.definition.delivery}`, into, depth);
    walk(producer.definition.body, producer.trace, block, eventScope(scope, producer), depth + 1, false);
    return;
  }
  const value = trace?.selected ? trace.text ?? "" : scope.values[name] ?? "";
  const body = el("div", { class: "body value" }, el("span", { class: "chip" }, el("span", { class: "chip-name" }, name)), el("span", { class: "input-value" }, value || "empty"));
  body.style.setProperty("--depth", String(depth));
  into.append(el("div", { class: "row input" }, el("div", { class: "gutter" }, el("span", { class: "path" }, "input")), body));
}

function nestedBlock(name, origin, into, depth) {
  const block = el("div", { class: "nested" });
  const label = el("div", { class: "nested-label" },
    el("span", { class: "chip nested" }, el("span", { class: "chip-name" }, name)),
    ` ${origin}`);
  label.style.setProperty("--depth", String(depth + 1));
  into.append(label, block);
  return block;
}

function usesTag(source) {
  const uses = source?.uses ?? [];
  if (!uses.length) return null;
  const list = el("ul", { class: "uses", hidden: true },
    ...uses.map((use) => el("li", {}, use.via ? `${use.event} via ${use.via}` : use.event)));
  const toggle = el("button", {
    class: "tag reach",
    type: "button",
    "aria-expanded": "false",
    onclick: () => {
      list.hidden = !list.hidden;
      toggle.setAttribute("aria-expanded", String(!list.hidden));
    },
  }, `+${uses.length} event${uses.length > 1 ? "s" : ""} off this page`);
  return el("span", { class: "reach-holder" }, toggle, list);
}

function countFragments(node) {
  if (node.kind === "text") return 1 + (node.bindings ?? []).reduce((sum, binding) => sum + countFragments(binding.node), 0);
  return (node.children ?? []).reduce((sum, child) => sum + countFragments(child), 0);
}

function deliversNothing(node) {
  if (node.kind === "text" || node.kind === "input") return false;
  return (node.children ?? []).every(deliversNothing);
}

function branchLabel(node, index) {
  const { field, test } = node.condition;
  const condition = test === "present" ? `${field.name} present` : field.name;
  if (node.kind === "when") return `when ${condition}`;
  return index === 0 ? `if ${condition}` : `unless ${condition}`;
}

function stub(node, index, child, childTrace, trace, scope, depth) {
  const field = node.condition.field;
  const empty = deliversNothing(child);
  const count = countFragments(child);
  const summary = empty ? "delivers nothing" : `${count} fragment${count === 1 ? "" : "s"} not delivered`;
  const holder = el("div", { class: "stub-holder" });
  holder.style.setProperty("--depth", String(depth));
  const expanded = el("div", { class: "stub-body", hidden: true });
  let drawn = false;
  const button = el("button", {
    class: "stub",
    type: "button",
    "data-stop": "",
    disabled: empty,
    "aria-expanded": "false",
    onclick: () => {
      if (!drawn) {
        walk(child, childTrace, expanded, scope, depth, true);
        drawn = true;
        scope.expanded();
      }
      expanded.hidden = !expanded.hidden;
      button.setAttribute("aria-expanded", String(!expanded.hidden));
    },
  }, el("span", { class: "stub-branch" }, branchLabel(node, index)), ` ${summary}`, trace?.reason ? el("span", { class: "stub-reason" }, ` · ${trace.reason}`) : null);
  const row = el("div", { class: "stub-row" }, button);
  if (field.kind === "flag") {
    const flipped = field.name in scope.flips;
    row.append(el("button", {
      class: "flip",
      type: "button",
      "aria-pressed": String(flipped),
      title: flipped ? `Return ${field.name} to the scenario's value` : `Show the page with ${field.name} flipped. Nothing is written.`,
      onclick: () => scope.flip(field.name, scope.values[field.name]),
    }, flipped ? `${field.name} flipped · undo` : `flip ${field.name}`));
  } else {
    row.append(el("span", { class: "stub-input" }, el("span", { class: "chip" }, el("span", { class: "chip-name" }, field.name), el("span", { class: "chip-value" }, oneLine(scope.values[field.name])))));
  }
  holder.append(row, expanded);
  return holder;
}

function oneLine(value) {
  const text = (value ?? "").replace(/\s+/g, " ").trim();
  if (!text) return "empty";
  return text.length > 60 ? text.slice(0, 59) + "…" : text;
}
