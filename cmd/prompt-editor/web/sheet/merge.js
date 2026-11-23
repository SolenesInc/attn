export function lines(text) {
  return text.match(/[^\n]*\n|[^\n]+$/g) ?? [];
}

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

export function changes(beforeText, afterText) {
  const before = beforeText.split("\n");
  const after = afterText.split("\n");
  const pairs = matching(before, after);
  const hunks = [];
  let i = 0;
  let j = 0;
  const flush = (toI, toJ) => {
    if (toI > i || toJ > j) hunks.push({ removed: before.slice(i, toI), added: [j, toJ] });
    i = toI;
    j = toJ;
  };
  for (const [bi, aj] of pairs) {
    flush(bi, aj);
    i++;
    j++;
  }
  flush(before.length, after.length);
  return hunks;
}

const same = (a, b) => a.length === b.length && a.every((line, index) => line === b[index]);

export function merge3(base, mine, theirs) {
  const b = lines(base);
  const m = lines(mine);
  const t = lines(theirs);
  const toMine = matching(b, m);
  const toTheirs = matching(b, t);
  const merged = [];
  let i = 0;
  let mi = 0;
  let ti = 0;
  while (i <= b.length) {
    let sync = i;
    while (sync < b.length && !(toMine.has(sync) && toTheirs.has(sync))) sync++;
    const mEnd = sync < b.length ? toMine.get(sync) : m.length;
    const tEnd = sync < b.length ? toTheirs.get(sync) : t.length;
    const baseChunk = b.slice(i, sync);
    const mineChunk = m.slice(mi, mEnd);
    const theirChunk = t.slice(ti, tEnd);
    if (same(mineChunk, baseChunk)) merged.push(...theirChunk);
    else if (same(theirChunk, baseChunk) || same(mineChunk, theirChunk)) merged.push(...mineChunk);
    else return { conflict: true };
    if (sync === b.length) break;
    merged.push(b[sync]);
    i = sync + 1;
    mi = mEnd + 1;
    ti = tEnd + 1;
  }
  return { conflict: false, text: merged.join("") };
}
