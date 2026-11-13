export class NativeAppDriver {
  constructor({ client, window }) {
    if (!client) {
      throw new Error('NativeAppDriver drives the app through its automation server; create it with createWindowDriver({ appPath, client })');
    }
    this.client = client;
    this.window = window;
  }

  get injectsInApp() {
    return true;
  }

  async #pointer(action, payload, opts) {
    const receipt = await this.client.request(action, payload);
    if (!receipt.matched && !opts.allowMiss) {
      throw new Error(`${action} on ${JSON.stringify(payload.target)} landed on ${receipt.landed ?? 'nothing'}; pass { allowMiss: true } when that is the point`);
    }
    return receipt;
  }

  click(target, opts = {}) {
    return this.#pointer('press', { target, ...(opts.at ? { at: opts.at } : {}) }, opts);
  }

  hover(target, opts = {}) {
    return this.#pointer('hover', { target, ...(opts.at ? { at: opts.at } : {}) }, opts);
  }

  drag(fromTarget, to, opts = {}) {
    const end = to?.by ? { by: to.by } : { to };
    return this.#pointer('drag', { target: fromTarget, ...end, ...(opts.at ? { at: opts.at } : {}) }, opts);
  }

  scroll(target, dy) {
    return this.client.request('scroll', { target, dy });
  }

  holdKeys(mods) {
    return this.client.request('hold', { mods });
  }

  pressKey(chord) {
    return this.client.request('key', { chord });
  }

  typeInto(target, text) {
    return this.client.request('type', { target, text });
  }

  run(name) {
    return this.client.request('run', { name });
  }

  find(target) {
    return this.client.request('find', { target });
  }

  waitFor(target, { gone = false, timeoutMs } = {}) {
    return this.client.request('wait', { target, gone, ...(timeoutMs ? { timeout_ms: timeoutMs } : {}) });
  }

  state(of, { includes, timeoutMs } = {}) {
    return this.client.request('state', { of, ...(includes ? { includes } : {}), ...(timeoutMs ? { timeout_ms: timeoutMs } : {}) });
  }

  tree() {
    return this.client.request('tree');
  }

  mainWindowId(opts) {
    return this.window.mainWindowId(opts);
  }

  waitForMainWindow(...args) {
    return this.window.waitForMainWindow(...args);
  }

  parkWindow(...args) {
    return this.window.parkWindow(...args);
  }

  setWindowBounds(...args) {
    return this.window.setWindowBounds(...args);
  }

  parkPointerBeside(...args) {
    return this.window.parkPointerBeside(...args);
  }

  async waitForTileWindow({ shown, timeoutMs = 10_000, pollIntervalMs = 100 }) {
    const deadline = Date.now() + timeoutMs;
    let last = [];
    while (Date.now() < deadline) {
      const { windows } = await this.client.request('native_windows', {});
      const visible = new Set(await this.window.shownWindowIds());
      last = windows.filter((w) => w.label !== 'main').map((w) => ({ ...w, shown: visible.has(w.id) }));
      if (last.length === 1 && last[0].shown === shown) return last[0];
      await new Promise((resolve) => setTimeout(resolve, pollIntervalMs));
    }
    throw new Error(`waited ${timeoutMs} ms for one tile window ${shown ? 'shown' : 'hidden'}; the app has ${JSON.stringify(last)}`);
  }

  async screenshot(outputPath) {
    const windowId = await this.mainWindowId();
    if (!windowId) {
      throw new Error('the native app reports no window to capture');
    }
    return this.window.screenshot(outputPath, { windowId });
  }

  frontmostBundleId() {
    return this.window.frontmostBundleId();
  }
}
