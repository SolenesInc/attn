export function mergedRecipients(current, base) {
  const recipients = new Map();
  for (const recipient of [...(current || []), ...(base || [])]) {
    const existing = recipients.get(recipient.id);
    if (!existing) recipients.set(recipient.id, { ...recipient, events: [...recipient.events] });
    else for (const event of recipient.events)
      if (!existing.events.some((candidate) => candidate.id === event.id)) existing.events.push(event);
  }
  return [...recipients.values()];
}

export function findEvent(catalog, key) {
  const [recipient, event] = key.split("/");
  return catalog?.recipients?.find((r) => r.id === recipient)?.events.find((e) => e.id === event);
}

export function sourceChange(current, base, path, text) {
  if (!base) return "";
  const was = Object.hasOwn(base.sources, path);
  const now = Object.hasOwn(current.sources, path);
  if (!was && now) return "added";
  if (was && !now) return "removed";
  return was && text !== base.sources[path].text ? "modified" : "";
}

export function diffRows(patch) {
  let oldLine = 0, newLine = 0, inHunk = false;
  const rows = [];
  for (const line of patch.split("\n")) {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      inHunk = true;
      rows.push({ kind: "hunk", text: line });
    } else if (inHunk && /^[ +\-\\]/.test(line)) {
      const kind = { "+": "added", "-": "removed", " ": "context", "\\": "notice" }[line[0]];
      const row = { kind, text: line.slice(1), marker: line[0] };
      if (kind === "removed" || kind === "context") row.oldLine = oldLine++;
      if (kind === "added" || kind === "context") row.newLine = newLine++;
      rows.push(row);
    }
  }
  return rows;
}

export function renderDiff(container, patch, message, baseLabel, currentLabel = "Working copy + drafts") {
  container.replaceChildren();
  const node = (tag, text, className) => {
    const el = document.createElement(tag);
    el.textContent = text;
    if (className) el.className = className;
    return el;
  };
  const legend = node("div", "", "diff-legend");
  legend.append(node("span", `− ${baseLabel || "Base"}`, "removed"), node("span", `+ ${currentLabel}`, "added"));
  container.append(legend);
  if (!patch) {
    container.append(node("p", message || "No changes.", "diff-empty"));
    return;
  }
  const rows = diffRows(patch);
  const added = rows.filter((r) => r.kind === "added").length;
  const removed = rows.filter((r) => r.kind === "removed").length;
  container.append(node("div", `${added} added · ${removed} removed`, "diff-summary"));
  const lines = node("div", "", "diff-lines");
  for (const row of rows) {
    const line = node("div", "", `diff-line ${row.kind}`);
    line.append(node("span", row.oldLine ?? "", "line-number"), node("span", row.newLine ?? "", "line-number"), node("span", row.marker || "", "diff-marker"), node("span", row.text, "line-text"));
    lines.append(line);
  }
  container.append(lines);
}
