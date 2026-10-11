interface PointerSample {
  clientX: number;
  clientY: number;
  target: string;
  matches: boolean;
}

interface PointerReceipt extends PointerSample {
  down?: PointerSample;
  sameTarget?: boolean;
  downTargetConnected?: boolean;
}

interface PointerWitness {
  selector: string;
  receipt: PointerReceipt | null;
  resolve: ((receipt: PointerReceipt) => void) | null;
  reject: ((error: Error) => void) | null;
  observe: (event: MouseEvent) => void;
  observeDown: (event: MouseEvent) => void;
}

let witness: PointerWitness | null = null;

function sample(event: MouseEvent, selector: string): PointerSample {
  const target = event.target instanceof Element ? event.target : null;
  return {
    clientX: event.clientX, clientY: event.clientY,
    target: target ? `${target.tagName.toLowerCase()}.${Array.from(target.classList).join('.')}` : 'none',
    matches: Boolean(target?.closest(selector)),
  };
}

export function disarmNativePointerWitness(): void {
  if (!witness) return;
  document.removeEventListener('mousedown', witness.observeDown, true);
  document.removeEventListener('mouseup', witness.observe, true);
  witness.reject?.(new Error(`Native pointer witness cancelled for ${witness.selector}`));
  witness = null;
}

export function armNativePointerWitness(selector: string): void {
  if (!document.querySelector(selector)) throw new Error(`Native pointer target not found: ${selector}`);
  disarmNativePointerWitness();
  let down: PointerSample | undefined;
  let downTarget: EventTarget | null = null;
  const armed: PointerWitness = {
    selector, receipt: null, resolve: null, reject: null,
    observeDown: event => {
      if (!event.isTrusted) return;
      down = sample(event, selector);
      downTarget = event.target;
    },
    observe: event => {
      if (!event.isTrusted) return;
      armed.receipt = {
        ...sample(event, selector),
        ...(down ? { down, sameTarget: downTarget === event.target, downTargetConnected: downTarget instanceof Node && downTarget.isConnected } : {}),
      };
      document.removeEventListener('mousedown', armed.observeDown, true);
      document.removeEventListener('mouseup', armed.observe, true);
      armed.resolve?.(armed.receipt);
      armed.resolve = null;
      armed.reject = null;
    },
  };
  witness = armed;
  document.addEventListener('mousedown', armed.observeDown, true);
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
