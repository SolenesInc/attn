type ChainDismissal = 'dismiss' | 'hover-leave' | 'outside-scroll' | 'outside-pointer' | 'handoff' | 'selection' | 'context' | 'anchor-removed' | 'sidebar-collapse';

interface FocusReturn {
  target: HTMLElement | null;
  navigationKey: string;
  restore: boolean;
  afterClose?: () => void;
}

export interface OpenChain {
  sessionId: string;
  navigationKey: string;
  anchor: HTMLElement | null;
  mode: 'hover' | 'pinned';
  focus: FocusReturn;
}

interface ChainScope {
  navigationKey: string;
  blocked: boolean;
  sessionIds: ReadonlySet<string>;
  onRestoreFocusFallback?: () => void;
}

const HOVER_CLOSE_DELAY_MS = 120;

export class DelegationChainController {
  private current: OpenChain | null = null;
  private listeners = new Set<() => void>();
  private scope: ChainScope = { navigationKey: '', blocked: false, sessionIds: new Set() };
  private closeTimer: ReturnType<typeof setTimeout> | undefined;
  private hoverSuppressed = false;
  private pointerPosition: { x: number; y: number } | null = null;
  private commandFocus: FocusReturn | null = null;
  private popover: HTMLElement | null = null;

  getSnapshot = () => this.current;

  get pinned() { return this.current?.mode === 'pinned'; }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  private publish(open: OpenChain | null) {
    this.current = open;
    this.listeners.forEach((listener) => listener());
  }

  configure(scope: ChainScope) {
    this.scope = scope;
    if (this.commandFocus?.navigationKey !== scope.navigationKey) this.commandFocus = null;
    if (this.current && !this.isVisible(this.current)) this.dismiss('context');
  }

  isVisible(open: OpenChain | null, scope: ChainScope = this.scope): open is OpenChain {
    return open !== null && !scope.blocked && open.navigationKey === scope.navigationKey && scope.sessionIds.has(open.sessionId);
  }

  connect() {
    window.addEventListener('pointermove', this.pointerMoved, true);
    window.addEventListener('pointerover', this.pointerMoved, true);
    window.addEventListener('pointerout', this.pointerMoved, true);
    document.addEventListener('pointerdown', this.outsidePointer, true);
    window.addEventListener('scroll', this.outsideScroll, true);
    return () => {
      this.cancelClose();
      window.removeEventListener('pointermove', this.pointerMoved, true);
      window.removeEventListener('pointerover', this.pointerMoved, true);
      window.removeEventListener('pointerout', this.pointerMoved, true);
      document.removeEventListener('pointerdown', this.outsidePointer, true);
      window.removeEventListener('scroll', this.outsideScroll, true);
    };
  }

  private pointerMoved = (event: PointerEvent) => {
    if (event.clientX === this.pointerPosition?.x && event.clientY === this.pointerPosition?.y) return;
    this.pointerPosition = { x: event.clientX, y: event.clientY };
    this.hoverSuppressed = false;
  };

  private outsidePointer = (event: PointerEvent) => {
    if (!this.current || this.popover?.contains(event.target as Node) || this.current.anchor?.contains(event.target as Node)) return;
    this.dismiss('outside-pointer');
  };

  private outsideScroll = (event: Event) => {
    if (this.current && !this.popover?.contains(event.target as Node)) this.dismiss('outside-scroll');
  };

  setPopover = (element: HTMLElement | null) => { this.popover = element; };

  cancelClose = () => {
    clearTimeout(this.closeTimer);
    this.closeTimer = undefined;
  };

  show(sessionId: string, anchor: HTMLElement, mode: OpenChain['mode']) {
    if (this.scope.blocked) return;
    if (mode === 'hover' && (this.hoverSuppressed || this.current?.mode === 'pinned')) return;
    this.cancelClose();
    const target = mode === 'pinned' ? anchor : this.current?.focus.target ?? document.activeElement as HTMLElement | null;
    this.activate(sessionId, anchor, mode, target);
  }

  private activate(sessionId: string, anchor: HTMLElement | null, mode: OpenChain['mode'], target: HTMLElement | null) {
    const previous = this.current;
    const navigationKey = this.scope.navigationKey;
    const samePopup = previous?.sessionId === sessionId && previous.navigationKey === navigationKey;
    const focus = samePopup ? previous.focus : { target, navigationKey, restore: true };
    if (previous && !samePopup) previous.focus.restore = false;
    focus.target = target;
    this.publish({ sessionId, navigationKey, anchor, mode, focus });
  }

  leave(sessionId: string, anchor: HTMLElement | null) {
    if (this.current?.sessionId !== sessionId || this.current.anchor !== anchor) return;
    this.cancelClose();
    this.closeTimer = setTimeout(() => {
      if (this.current?.mode === 'hover' && this.current.sessionId === sessionId && this.current.anchor === anchor) this.dismiss('hover-leave');
    }, HOVER_CLOSE_DELAY_MS);
  }

  detach(anchor: HTMLElement) {
    if (this.current?.anchor === anchor) this.dismiss('anchor-removed');
  }

  prepareCommand = () => {
    this.commandFocus = {
      target: this.current?.focus.target ?? document.activeElement as HTMLElement | null,
      navigationKey: this.scope.navigationKey,
      restore: false,
    };
    this.dismiss('handoff');
  };

  open = (sessionId: string, returnFocus?: HTMLElement | null) => {
    this.cancelClose();
    const anchor = Array.from(document.querySelectorAll<HTMLElement>('.delegation-chain-trigger--header'))
      .find((element) => element.dataset.delegationSession === sessionId && element.getClientRects().length > 0) ?? null;
    this.activate(sessionId, anchor, 'pinned', returnFocus ?? this.commandFocus?.target ?? anchor);
    this.commandFocus = null;
  };

  dismiss = (reason: ChainDismissal = 'dismiss') => {
    this.cancelClose();
    if (!this.current) return;
    this.hoverSuppressed = reason !== 'hover-leave';
    this.current.focus.restore = !['outside-pointer', 'handoff', 'selection', 'context'].includes(reason);
    if (reason === 'sidebar-collapse') this.current.focus.target = null;
    this.publish(null);
  };

  select(sessionId: string, onSelect: (sessionId: string) => void) {
    if (!this.current) return;
    this.current.focus.afterClose = () => onSelect(sessionId);
    this.dismiss('selection');
  }

  finishClose(focus: FocusReturn) {
    const afterClose = focus.afterClose;
    focus.afterClose = undefined;
    if (this.current || this.scope.blocked || focus.navigationKey !== this.scope.navigationKey) return;
    afterClose?.();
  }

  returnFocus(focus: FocusReturn): HTMLElement | false {
    if (!focus.restore || this.current || this.scope.blocked || focus.navigationKey !== this.scope.navigationKey) return false;
    focus.restore = false;
    if (focus.target?.isConnected) return focus.target;
    this.scope.onRestoreFocusFallback?.();
    return false;
  }
}
