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

  const greedyMatches = (reverse, oldStart, oldStop, newStart, newStop) => {
    const positions = new Map(), offsets = new Map(), matches = [];
    for (let i = newStart; i < newStop; i++) {
      if (!positions.has(newTokens[i])) positions.set(newTokens[i], []);
      positions.get(newTokens[i]).push(i);
    }
    let nextPosition = reverse ? newStop : newStart - 1;
    for (let i = reverse ? oldStop - 1 : oldStart; reverse ? i >= oldStart : i < oldStop; i += reverse ? -1 : 1) {
      const available = positions.get(oldTokens[i]) || [];
      let offset = offsets.get(oldTokens[i]) ?? (reverse ? available.length - 1 : 0);
      while (reverse ? offset >= 0 && available[offset] >= nextPosition : offset < available.length && available[offset] <= nextPosition) offset += reverse ? -1 : 1;
      if (offset >= 0 && offset < available.length) {
        matches.push({ old: i, next: available[offset] });
        nextPosition = available[offset];
        offset += reverse ? -1 : 1;
      }
      offsets.set(oldTokens[i], offset);
    }
    return reverse ? matches.reverse() : matches;
  };
  const bestGreedy = (oldStart, oldStop, newStart, newStop) => {
    const forward = greedyMatches(false, oldStart, oldStop, newStart, newStop);
    const backward = greedyMatches(true, oldStart, oldStop, newStart, newStop);
    return forward.length >= backward.length ? forward : backward;
  };
  // Keep the alignment that preserves more words across repeated or moved phrases.
  let matches = bestGreedy(prefix, oldEnd, prefix, newEnd);

  const oldUnique = new Map(), newUnique = new Map();
  for (let old = prefix; old < oldEnd; old++) oldUnique.set(oldTokens[old], oldUnique.has(oldTokens[old]) ? -1 : old);
  for (let next = prefix; next < newEnd; next++) newUnique.set(newTokens[next], newUnique.has(newTokens[next]) ? -1 : next);
  const candidates = [];
  for (let old = prefix; old < oldEnd; old++) {
    const next = newUnique.get(oldTokens[old]);
    if (oldUnique.get(oldTokens[old]) === old && next >= 0) candidates.push({ old, next });
  }
  const tails = [], tailIndices = [], previous = new Int32Array(candidates.length).fill(-1);
  for (let i = 0; i < candidates.length; i++) {
    let low = 0, high = tails.length;
    while (low < high) {
      const middle = (low + high) >> 1;
      if (tails[middle] < candidates[i].next) low = middle + 1;
      else high = middle;
    }
    if (low) previous[i] = tailIndices[low - 1];
    tails[low] = candidates[i].next;
    tailIndices[low] = i;
  }
  const anchors = [];
  for (let i = tailIndices.at(-1); i !== undefined && i >= 0; i = previous[i]) anchors.push(candidates[i]);
  anchors.reverse();
  const anchored = [];
  let gapOld = prefix, gapNew = prefix;
  for (const anchor of [...anchors, { old: oldEnd, next: newEnd }]) {
    anchored.push(...bestGreedy(gapOld, anchor.old, gapNew, anchor.next));
    if (anchor.old < oldEnd) anchored.push(anchor);
    gapOld = anchor.old + 1;
    gapNew = anchor.next + 1;
  }
  if (anchored.length > matches.length) matches = anchored;

  let old = 0, next = 0;
  for (let i = 0; i < prefix; i++) {
    append(removed, oldTokens[i], false);
    append(added, newTokens[i], false);
    old++;
    next++;
  }
  for (const anchor of [...matches, { old: oldEnd, next: newEnd }]) {
    if (old < anchor.old) append(removed, oldTokens.slice(old, anchor.old).join(""), true);
    if (next < anchor.next) append(added, newTokens.slice(next, anchor.next).join(""), true);
    if (anchor.old < oldEnd) {
      append(removed, oldTokens[anchor.old], false);
      append(added, newTokens[anchor.next], false);
    }
    old = anchor.old + 1;
    next = anchor.next + 1;
  }
  for (let i = 0; i < oldTokens.length - oldEnd; i++) {
    append(removed, oldTokens[oldEnd + i], false);
    append(added, newTokens[newEnd + i], false);
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
