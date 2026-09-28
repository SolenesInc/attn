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

// The catalog's longest line has 225 tokens; 1M LCS cells took 4ms locally.
const WORD_WORK_LIMIT = 4_000_000;
// Catalog lines average 14 words; 1M row-word checks is roughly 60ms locally.
const ROW_WORK_LIMIT = 1_000_000;

function changedParts(before, after, remainingWork) {
  const oldTokens = before.match(/\s+|\S+\s*/g) || [];
  const newTokens = after.match(/\s+|\S+\s*/g) || [];
  const removed = [], added = [];
  const append = (parts, text, changed) => {
    const last = parts.at(-1);
    if (last?.changed === changed) last.text += text;
    else parts.push({ text, changed });
  };
  let prefix = 0;
  while (prefix < oldTokens.length && prefix < newTokens.length && oldTokens[prefix] === newTokens[prefix]) prefix++;
  let oldEnd = oldTokens.length, newEnd = newTokens.length;
  while (oldEnd > prefix && newEnd > prefix && oldTokens[oldEnd - 1] === newTokens[newEnd - 1]) {
    oldEnd--;
    newEnd--;
  }
  const oldLength = oldEnd - prefix, newLength = newEnd - prefix;
  const work = oldLength * newLength;
  if (work > remainingWork) return { work };
  const matches = Array.from({ length: oldLength + 1 }, () => new Uint32Array(newLength + 1));
  for (let old = oldLength - 1; old >= 0; old--) {
    for (let next = newLength - 1; next >= 0; next--) {
      matches[old][next] = oldTokens[prefix + old] === newTokens[prefix + next]
        ? matches[old + 1][next + 1] + 1
        : Math.max(matches[old + 1][next], matches[old][next + 1]);
    }
  }

  let old = 0, next = 0;
  for (let i = 0; i < prefix; i++) {
    append(removed, oldTokens[i], false);
    append(added, newTokens[i], false);
    old++;
    next++;
  }
  while (old < oldEnd || next < newEnd) {
    if (old < oldEnd && next < newEnd && oldTokens[old] === newTokens[next]) {
      append(removed, oldTokens[old++], false);
      append(added, newTokens[next++], false);
    } else if (old < oldEnd && (next === newEnd || matches[old - prefix + 1][next - prefix] >= matches[old - prefix][next - prefix + 1])) {
      append(removed, oldTokens[old++], true);
    } else {
      append(added, newTokens[next++], true);
    }
  }
  for (let i = 0; i < oldTokens.length - oldEnd; i++) {
    append(removed, oldTokens[oldEnd + i], false);
    append(added, newTokens[newEnd + i], false);
  }
  return { parts: [removed, added], work };
}

function sharedWords(before, after) {
  const remaining = new Map();
  for (const word of before) remaining.set(word, (remaining.get(word) || 0) + 1);
  let count = 0;
  for (const word of after) {
    const available = remaining.get(word) || 0;
    if (available) {
      count++;
      remaining.set(word, available - 1);
    }
  }
  return count;
}

function highlightChangedRows(rows) {
  const skipped = { rowBlocks: 0, wordPairs: 0, rowAsk: 0, wordAsk: 0 };
  let rowWork = 0, wordWork = 0;
  for (let start = 0; start < rows.length;) {
    if (rows[start].kind !== "removed") { start++; continue; }
    let end = start;
    while (["removed", "added", "notice"].includes(rows[end]?.kind)) end++;
    const removed = rows.slice(start, end).filter((row) => row.kind === "removed");
    const added = rows.slice(start, end).filter((row) => row.kind === "added");
    const oldWords = removed.map((row) => row.text.match(/\S+/g) || []);
    const newWords = added.map((row) => row.text.match(/\S+/g) || []);
    const requested = removed.length * added.length
      + added.length * oldWords.reduce((total, words) => total + words.length, 0)
      + removed.length * newWords.reduce((total, words) => total + words.length, 0);
    if (rowWork + requested > ROW_WORK_LIMIT) {
      skipped.rowBlocks++;
      skipped.rowAsk = Math.max(skipped.rowAsk, rowWork + requested);
      start = end;
      continue;
    }
    rowWork += requested;
    const scores = oldWords.map((old) => newWords.map((next) => sharedWords(old, next)));
    const best = Array.from({ length: removed.length + 1 }, () => new Uint32Array(added.length + 1));
    for (let old = removed.length - 1; old >= 0; old--) {
      for (let next = added.length - 1; next >= 0; next--) {
        best[old][next] = Math.max(best[old + 1][next], best[old][next + 1], scores[old][next] + best[old + 1][next + 1]);
      }
    }
    let old = 0, next = 0;
    while (old < removed.length && next < added.length) {
      if (scores[old][next] && best[old][next] === scores[old][next] + best[old + 1][next + 1]) {
        const result = changedParts(removed[old].text, added[next].text, WORD_WORK_LIMIT - wordWork);
        if (result.parts) {
          [removed[old].parts, added[next].parts] = result.parts;
          wordWork += result.work;
        } else {
          skipped.wordPairs++;
          skipped.wordAsk = Math.max(skipped.wordAsk, wordWork + result.work);
        }
        old++;
        next++;
      } else if (best[old + 1][next] >= best[old][next + 1]) old++;
      else next++;
    }
    start = end;
  }
  return skipped;
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
  const skipped = highlightChangedRows(rows);
  const added = rows.filter((r) => r.kind === "added").length;
  const removed = rows.filter((r) => r.kind === "removed").length;
  container.append(node("div", `${added} added · ${removed} removed`, "diff-summary"));
  if (skipped.rowBlocks) container.append(node("div", `Word detail skipped in ${skipped.rowBlocks} changed block(s): line pairing work limit ${ROW_WORK_LIMIT.toLocaleString()}, requested ${skipped.rowAsk.toLocaleString()}. Full-line changes remain visible.`, "diff-limit"));
  if (skipped.wordPairs) container.append(node("div", `Word detail skipped in ${skipped.wordPairs} line pair(s): word comparison limit ${WORD_WORK_LIMIT.toLocaleString()} cells, requested ${skipped.wordAsk.toLocaleString()}. Full-line changes remain visible.`, "diff-limit"));
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
