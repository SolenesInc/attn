interface PointerReceipt {
  clientX: number;
  clientY: number;
  target: string;
  matches: boolean;
}

interface PointerWitness {
  selector: string;
  receipt: PointerReceipt | null;
  resolve: ((receipt: PointerReceipt) => void) | null;
  reject: ((error: Error) => void) | null;
  observe: (event: MouseEvent) => void;
}

let witness: PointerWitness | null = null;

export function disarmNativePointerWitness(): void {
  if (!witness) return;
  document.removeEventListener('mouseup', witness.observe, true);
  witness.reject?.(new Error(`Native pointer witness cancelled for ${witness.selector}`));
  witness = null;
}

// One trusted mouseup acknowledges OS delivery, even when it hit the wrong element.
export function armNativePointerWitness(selector: string): void {
  if (!document.querySelector(selector)) throw new Error(`Native pointer target not found: ${selector}`);
  disarmNativePointerWitness();
  const armed: PointerWitness = {
    selector, receipt: null, resolve: null, reject: null,
    observe: event => {
      if (!event.isTrusted) return;
      const target = event.target instanceof Element ? event.target : null;
      armed.receipt = {
        clientX: event.clientX, clientY: event.clientY,
        target: target ? `${target.tagName.toLowerCase()}.${Array.from(target.classList).join('.')}` : 'none',
        matches: Boolean(target?.closest(selector)),
      };
      document.removeEventListener('mouseup', armed.observe, true);
      armed.resolve?.(armed.receipt);
      armed.resolve = null;
      armed.reject = null;
    },
  };
  witness = armed;
  document.addEventListener('mouseup', armed.observe, true);
}

export function waitForNativePointerWitness(): Promise<PointerReceipt> {
  const armed = witness;
  if (!armed) return Promise.reject(new Error('Native pointer witness is not armed'));
  if (armed.receipt) return Promise.resolve(armed.receipt);
  if (armed.resolve) return Promise.reject(new Error(`Native pointer witness already has a reader: ${armed.selector}`));
  return new Promise((resolve, reject) => {
    armed.resolve = resolve;
    armed.reject = reject;
  });
}
