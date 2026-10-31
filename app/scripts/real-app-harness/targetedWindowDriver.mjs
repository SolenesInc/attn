// Field focus normally lands within one frame; this guards a hung UI, like the scenarios' 15s dom_wait.
const FOCUS_LIMIT_MS = 15_000;
const ENTER_KEY_CODE = 36;

function selectorOf(target) {
  return typeof target === 'string' ? target : target.selector ?? null;
}

function webviewOf(target) {
  const webview = typeof target === 'string' ? undefined : target.webview;
  if (webview !== undefined && webview !== 'capture') {
    throw new Error(`Unknown target webview ${JSON.stringify(webview)}; targets resolve in the main webview or in "capture"`);
  }
  return webview ?? 'main';
}

export class TargetedWindowDriver {
  #pointer;
  #keyboard;

  constructor({ client, pointer, keyboard }) {
    this.client = client;
    this.#pointer = pointer;
    this.#keyboard = keyboard;
  }

  async pointOf(target) {
    if (!this.client) {
      throw new Error('Native input on a target needs the automation client: createWindowDriver({ appPath, client })');
    }
    const { webview: _webview, ...request } = typeof target === 'string' ? { selector: target } : target;
    return this.client.request(webviewOf(target) === 'capture' ? 'capture_native_point' : 'dom_native_point', request);
  }

  async #delivered({ webview, expectation, action, opts }, act) {
    if (!this.client || opts.withoutReceipt) {
      await act();
      return null;
    }
    const prefix = webview === 'capture' ? 'capture_' : '';
    await this.client.request(`${prefix}arm_native_input_witness`, expectation);
    await act();
    try {
      return await this.client.request(`${prefix}wait_native_input_witness`, {});
    } catch (error) {
      throw new Error(`${action} was sent but the app did not receive it: ${error?.message || error}. `
        + 'Input that lands in a browser tile page, or that replaces the page receiving it, has no receipt; '
        + 'pass { withoutReceipt: \'<why>\' } for it.', { cause: error });
    }
  }

  async #press(target, action, opts) {
    const point = await this.pointOf(target);
    const receipt = await this.#delivered({
      webview: webviewOf(target),
      expectation: { gesture: 'pointer', selector: selectorOf(target) },
      action: `${action}(${JSON.stringify(target)})`,
      opts,
    }, () => this.#pointer({ action, point, opts }));
    return { ...point, receipt };
  }

  async click(target, opts = {}) {
    return this.#press(target, 'click', opts);
  }

  async rightClick(target, opts = {}) {
    return this.#press(target, 'right_click', opts);
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
    const receipt = await this.#delivered({
      webview: webviewOf(fromTarget),
      expectation: { gesture: 'pointer', selector: toTarget.by ? null : selectorOf(toTarget) },
      action: `drag(${JSON.stringify(fromTarget)}, ${JSON.stringify(toTarget)})`,
      opts,
    }, () => this.#pointer({ action: 'drag', point: from, to, opts }));
    return { from, to, receipt };
  }

  async #keys(stroke, strokes, action, opts) {
    return this.#delivered({
      webview: 'main',
      expectation: { gesture: 'keys', strokes },
      action,
      opts,
    }, () => this.#keyboard(stroke));
  }

  async pressKey(key, modifiers = {}, opts = {}) {
    return this.#keys({ kind: 'key', key, modifiers }, 1, `pressKey(${JSON.stringify(key)}, ${JSON.stringify(modifiers)})`, opts);
  }

  async pressKeyCode(keyCode, modifiers = {}, opts = {}) {
    return this.#keys({ kind: 'keycode', keyCode, modifiers }, 1, `pressKeyCode(${keyCode}, ${JSON.stringify(modifiers)})`, opts);
  }

  async pressEnter(opts = {}) {
    return this.pressKeyCode(ENTER_KEY_CODE, {}, opts);
  }

  async typeText(text, opts = {}) {
    const value = String(text);
    return this.#keys({ kind: 'text', text: value }, [...value].length, `typeText(${JSON.stringify(value.slice(0, 40))})`, opts);
  }

  async typeInto(target, text, opts = {}) {
    if (webviewOf(target) !== 'main') {
      throw new Error(`typeInto waits for focus in the main webview; ${JSON.stringify(target)} is in ${target.webview}`);
    }
    const point = await this.click(target, opts);
    await this.client.request('dom_wait', { selector: selectorOf(target), focused: true, timeoutMs: FOCUS_LIMIT_MS });
    await this.typeText(text, opts);
    return point;
  }
}
