export interface AutomationDomExpectation {
  selector: string;
  absent?: boolean;
  textIncludes?: string;
  valueEquals?: string;
  focused?: boolean;
  timeoutMs: number;
}

export function waitForAutomationDom({ selector, absent = false, textIncludes, valueEquals, focused = false, timeoutMs }: AutomationDomExpectation): Promise<{ matched: true }> {
  if (!selector) throw new Error('dom_wait requires selector');
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Error(`dom_wait requires a positive timeoutMs; received ${timeoutMs}`);
  document.querySelector(selector);
  return new Promise((resolve, reject) => {
    const observer = new MutationObserver(check);
    const timeout = window.setTimeout(() => {
      cleanup();
      reject(new Error(`dom_wait timeoutMs=${timeoutMs} exceeded: selector=${selector}, absent=${absent}, textIncludes=${JSON.stringify(textIncludes)}, valueEquals=${JSON.stringify(valueEquals)}, focused=${focused}`));
    }, timeoutMs);
    function cleanup() {
      observer.disconnect();
      document.removeEventListener('focusin', check);
      document.removeEventListener('focusout', check);
      document.removeEventListener('input', check, true);
      document.removeEventListener('change', check, true);
      window.clearTimeout(timeout);
    }
    function check() {
      const element = document.querySelector(selector);
      const matches = absent ? !element : Boolean(element
        && (textIncludes === undefined || element.textContent?.includes(textIncludes))
        && (valueEquals === undefined || (element as HTMLInputElement).value === valueEquals)
        && (!focused || document.activeElement === element));
      if (!matches) return;
      cleanup();
      resolve({ matched: true });
    }
    observer.observe(document.documentElement, { subtree: true, childList: true, attributes: true, characterData: true });
    document.addEventListener('focusin', check);
    document.addEventListener('focusout', check);
    document.addEventListener('input', check, true);
    document.addEventListener('change', check, true);
    check();
  });
}
