// Field focus normally lands within one frame; this guards a hung UI, like the scenarios' 15s dom_wait.
const FOCUS_LIMIT_MS = 15_000;

export class TargetedWindowDriver {
  #pointer;

  constructor({ client, pointer }) {
    this.client = client;
    this.#pointer = pointer;
  }

  async pointOf(target) {
    if (!this.client) {
      throw new Error('Native input on a target needs the automation client: createWindowDriver({ appPath, client })');
    }
    const { webview, ...request } = typeof target === 'string' ? { selector: target } : target;
    if (webview !== undefined && webview !== 'capture') {
      throw new Error(`Unknown target webview ${JSON.stringify(webview)}; targets resolve in the main webview or in "capture"`);
    }
    return this.client.request(webview === 'capture' ? 'capture_native_point' : 'dom_native_point', request);
  }

  async click(target, opts = {}) {
    const point = await this.pointOf(target);
    await this.#pointer({ action: 'click', point, opts });
    return point;
  }

  async rightClick(target, opts = {}) {
    const point = await this.pointOf(target);
    await this.#pointer({ action: 'right_click', point, opts });
    return point;
  }

  async hover(target, opts = {}) {
    const point = await this.pointOf(target);
    await this.#pointer({ action: 'move', point, opts });
    return point;
  }

  async scroll(target, deltaY, opts = {}) {
    const point = await this.pointOf(target);
    await this.#pointer({ action: 'scroll', point, deltaY, opts });
    return point;
  }

  async drag(fromTarget, toTarget, opts = {}) {
    let from = await this.pointOf(fromTarget);
    const to = toTarget.by
      ? { x: from.x + toTarget.by.x / from.window.width, y: from.y + toTarget.by.y / from.window.height }
      : await this.pointOf(toTarget);
    if (to.scrolled) {
      from = await this.pointOf(fromTarget);
      if (from.scrolled) {
        throw new Error(`Cannot drag ${JSON.stringify(fromTarget)} to ${JSON.stringify(toTarget)}: the page cannot show both ends at once`);
      }
    }
    await this.#pointer({ action: 'drag', point: from, to, opts });
    return { from, to };
  }

  async typeInto(target, text, opts = {}) {
    if (target.webview !== undefined) {
      throw new Error(`typeInto waits for focus in the main webview; ${JSON.stringify(target)} is in ${target.webview}`);
    }
    const point = await this.click(target, opts);
    const selector = typeof target === 'string' ? target : target.selector;
    await this.client.request('dom_wait', { selector, focused: true, timeoutMs: FOCUS_LIMIT_MS });
    await this.typeText(text);
    return point;
  }
}
