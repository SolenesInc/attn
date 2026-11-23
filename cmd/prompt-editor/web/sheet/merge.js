const lines = (text) => (text === "" ? [] : text.split("\n"));

export function matching(before, after) {
  const lengths = Array.from({ length: before.length + 1 }, () => new Uint32Array(after.length + 1));
  for (let i = before.length - 1; i >= 0; i--)
    for (let j = after.length - 1; j >= 0; j--)
      lengths[i][j] = before[i] === after[j] ? lengths[i + 1][j + 1] + 1 : Math.max(lengths[i + 1][j], lengths[i][j + 1]);
  const pairs = new Map();
  let i = 0;
  let j = 0;
  while (i < before.length && j < after.length) {
    if (before[i] === after[j]) pairs.set(i++, j++);
    else if (lengths[i + 1][j] >= lengths[i][j + 1]) i++;
    else j++;
  }
  return pairs;
}

function hunks(before, after) {
  const found = [];
  let i = 0;
  let j = 0;
  const flush = (toI, toJ) => {
    if (toI > i || toJ > j) found.push({ from: i, to: toI, start: j, end: toJ });
    i = toI + 1;
    j = toJ + 1;
  };
  for (const [bi, aj] of matching(before, after)) flush(bi, aj);
  flush(before.length, after.length);
  return found;
}

export function changes(beforeText, afterText) {
  const before = lines(beforeText);
  return hunks(before, lines(afterText)).map(({ from, to, start, end }) => ({ removed: before.slice(from, to), added: [start, end] }));
}

function sideHunks(base, side, mine) {
  return hunks(base, side).map(({ from, to, start, end }) => ({ from, to, lines: side.slice(start, end), mine }));
}

function overlap(a, b) {
  if (a.from === a.to && b.from === b.to) return a.from === b.from;
  return a.from < b.to && b.from < a.to;
}

function regions(mine, theirs) {
  const all = [...mine, ...theirs].sort((a, b) => a.from - b.from || a.to - b.to);
  const found = [];
  for (const hunk of all) {
    const last = found.at(-1);
    if (last && overlap(last, hunk)) {
      last.to = Math.max(last.to, hunk.to);
      last.hunks.push(hunk);
    } else found.push({ from: hunk.from, to: hunk.to, hunks: [hunk] });
  }
  return found;
}

function sideOf(base, region, mine) {
  const out = [];
  let at = region.from;
  for (const hunk of region.hunks) {
    if (hunk.mine !== mine) continue;
    out.push(...base.slice(at, hunk.from), ...hunk.lines);
    at = hunk.to;
  }
  out.push(...base.slice(at, region.to));
  return out;
}

const same = (a, b) => a.length === b.length && a.every((line, index) => line === b[index]);

function merge(baseText, mineText, theirsText) {
  const base = lines(baseText);
  const mine = lines(mineText);
  const theirs = lines(theirsText);
  const merged = [];
  let conflict = false;
  let at = 0;
  for (const region of regions(sideHunks(base, mine, true), sideHunks(base, theirs, false))) {
    merged.push(...base.slice(at, region.from));
    const ours = sideOf(base, region, true);
    const disk = sideOf(base, region, false);
    const mineChanged = region.hunks.some((hunk) => hunk.mine);
    const diskChanged = region.hunks.some((hunk) => !hunk.mine);
    if (mineChanged && diskChanged && !same(ours, disk)) conflict = true;
    merged.push(...(mineChanged ? ours : disk));
    at = region.to;
  }
  merged.push(...base.slice(at));
  return { conflict, text: merged.join("\n") };
}

export function merge3(base, mine, theirs) {
  const { conflict, text } = merge(base, mine, theirs);
  return conflict ? { conflict: true } : { conflict: false, text };
}

export function mergeKeepingMine(base, mine, theirs) {
  return merge(base, mine, theirs).text;
}
