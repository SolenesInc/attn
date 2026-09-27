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

function changedParts(before, after) {
  const oldTokens = before.match(/\s+|\S+\s*/g) || [];
  const newTokens = after.match(/\s+|\S+\s*/g) || [];
  const matches = Array.from({ length: oldTokens.length + 1 }, () => new Uint32Array(newTokens.length + 1));
  for (let old = oldTokens.length - 1; old >= 0; old--) {
    for (let next = newTokens.length - 1; next >= 0; next--) {
      matches[old][next] = oldTokens[old] === newTokens[next]
        ? matches[old + 1][next + 1] + 1
        : Math.max(matches[old + 1][next], matches[old][next + 1]);
    }
  }
  const removed = [], added = [];
  const append = (parts, text, changed) => {
    const last = parts.at(-1);
    if (last?.changed === changed) last.text += text;
    else parts.push({ text, changed });
  };
  let old = 0, next = 0;
  while (old < oldTokens.length || next < newTokens.length) {
    if (old < oldTokens.length && next < newTokens.length && oldTokens[old] === newTokens[next]) {
      append(removed, oldTokens[old], false);
      append(added, newTokens[next], false);
      old++;
      next++;
    } else if (old < oldTokens.length && (next === newTokens.length || matches[old + 1][next] >= matches[old][next + 1])) {
      append(removed, oldTokens[old++], true);
    } else {
      append(added, newTokens[next++], true);
    }
  }
  return [removed, added];
}

function sharedWords(before, after) {
  const remaining = new Map();
  for (const word of before.match(/\S+/g) || []) remaining.set(word, (remaining.get(word) || 0) + 1);
  let count = 0;
  for (const word of after.match(/\S+/g) || []) {
    const available = remaining.get(word) || 0;
    if (available) {
      count++;
      remaining.set(word, available - 1);
    }
  }
  return count;
}

function highlightChangedRows(rows) {
  for (let start = 0; start < rows.length;) {
    if (rows[start].kind !== "removed") { start++; continue; }
    let end = start;
    while (["removed", "added", "notice"].includes(rows[end]?.kind)) end++;
    const removed = rows.slice(start, end).filter((row) => row.kind === "removed");
    const added = rows.slice(start, end).filter((row) => row.kind === "added");
    const scores = removed.map((old) => added.map((next) => sharedWords(old.text, next.text)));
    const best = Array.from({ length: removed.length + 1 }, () => new Uint32Array(added.length + 1));
    for (let old = removed.length - 1; old >= 0; old--) {
      for (let next = added.length - 1; next >= 0; next--) {
        best[old][next] = Math.max(best[old + 1][next], best[old][next + 1], scores[old][next] + best[old + 1][next + 1]);
      }
    }
    let old = 0, next = 0;
    while (old < removed.length && next < added.length) {
      if (scores[old][next] && best[old][next] === scores[old][next] + best[old + 1][next + 1]) {
        [removed[old].parts, added[next].parts] = changedParts(removed[old].text, added[next].text);
        old++;
        next++;
      } else if (best[old + 1][next] >= best[old][next + 1]) old++;
      else next++;
    }
    start = end;
  }
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
  highlightChangedRows(rows);
  const added = rows.filter((r) => r.kind === "added").length;
  const removed = rows.filter((r) => r.kind === "removed").length;
  container.append(node("div", `${added} added · ${removed} removed`, "diff-summary"));
  const lines = node("div", "", "diff-lines");
  for (const row of rows) {
    const line = node("div", "", `diff-line ${row.kind}`);
    const text = node("span", "", "line-text");
    if (row.parts) for (const part of row.parts) text.append(node("span", part.text, part.changed ? "changed-text" : ""));
    else text.textContent = row.text;
    line.append(node("span", row.oldLine ?? "", "line-number"), node("span", row.newLine ?? "", "line-number"), node("span", row.marker || "", "diff-marker"), text);
    lines.append(line);
  }
  container.append(lines);
}
