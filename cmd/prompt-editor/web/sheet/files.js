export function sliceRange(text, node) {
  let from = 0;
  let to = text.length;
  if (node.part) {
    const at = text.indexOf(node.part.marker);
    if (at >= 0 && node.part.index === 0) to = at;
    if (at >= 0 && node.part.index === 1) from = at + node.part.marker.length;
  }
  if (!node.keep_final_newline && to > from && text[to - 1] === "\n") to--;
  return [from, to];
}

export function sliceOf(text, node) {
  const [from, to] = sliceRange(text, node);
  return text.slice(from, to);
}

export class Files {
  #files = new Map();
  #fragments = new Map();
  #listeners = new Set();

  take(sources) {
    for (const [path, source] of Object.entries(sources)) {
      const file = this.#files.get(path);
      if (!file) this.#files.set(path, { disk: source.text, revision: source.revision, local: source.text, conflict: null });
      else if (!this.dirty(path)) Object.assign(file, { disk: source.text, revision: source.revision, local: source.text });
    }
    this.#notify();
  }

  reloadDisk(sources) {
    for (const [path, source] of Object.entries(sources)) {
      const file = this.#files.get(path);
      if (file && !file.conflict) Object.assign(file, { disk: source.text, revision: source.revision });
    }
    this.#notify();
  }

  text(path) {
    return this.#files.get(path).local;
  }

  conflict(path) {
    return this.#files.get(path)?.conflict ?? null;
  }

  dirty(path) {
    const file = this.#files.get(path);
    return Boolean(file && (file.local !== file.disk || file.conflict));
  }

  dirtyPaths() {
    return [...this.#files.keys()].filter((path) => this.dirty(path)).sort();
  }

  saveRequest() {
    const files = [];
    for (const [path, file] of this.#files)
      if (file.local !== file.disk && !file.conflict) files.push({ path, text: file.local, revision: file.revision });
    return files;
  }

  saved(path, text, revision) {
    Object.assign(this.#files.get(path), { disk: text, revision });
    this.#notify();
  }

  conflicted(path, disk) {
    this.#files.get(path).conflict = disk;
    this.#notify();
  }

  attach(path, node, view, replace) {
    if (!this.#fragments.has(path)) this.#fragments.set(path, new Set());
    this.#fragments.get(path).add({ node, view, replace });
  }

  detachAll() {
    this.#fragments.clear();
  }

  fragmentsOf(path) {
    return this.#fragments.get(path)?.size ?? 0;
  }

  edit(path, node, fragmentText, from) {
    const file = this.#files.get(path);
    const [start, end] = sliceRange(file.local, node);
    file.local = file.local.slice(0, start) + fragmentText + file.local.slice(end);
    for (const fragment of this.#fragments.get(path) ?? [])
      if (fragment.view !== from) fragment.replace(sliceOf(file.local, fragment.node));
    this.#notify();
  }

  listen(listener) {
    this.#listeners.add(listener);
  }

  #notify() {
    for (const listener of this.#listeners) listener();
  }
}
