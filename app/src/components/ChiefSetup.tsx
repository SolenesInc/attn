import { useCallback, useEffect, useEffectEvent, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react';
import { createPortal } from 'react-dom';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useHarnesses } from '../hooks/useHarnesses';
import { routeFromStored, useHarnessRoute, type HarnessRoute } from '../hooks/useHarnessRoute';
import type { ChiefView } from '../application/useChief';
import { chiefHarnesses, chiefRouteRules } from './chiefRoute';

export function ChiefSetup({ chief, anchor, onSave, onClose, onReturnFocus, onOpenSettings }: {
  chief: ChiefView;
  anchor: HTMLElement;
  onSave: (route: HarnessRoute, notebookRoot?: string) => Promise<boolean>;
  onClose: (returnFocus?: boolean) => void;
  onReturnFocus: () => void;
  onOpenSettings?: () => void;
}) {
  const { settings, sendGetSettings } = useDaemonApi();
  const [firstRun] = useState(!chief.agent);
  const [notebookRoot, setNotebookRoot] = useState<string | null>(null);
  const folder = notebookRoot ?? String(settings['notebook.root'] || '');
  const catalog = useHarnesses();
  const harnesses = chiefHarnesses(catalog.harnesses, settings);
  const defaultHarness = harnesses.find(harness => harness.id === settings.new_session_agent) || harnesses[0];
  const [draft, setDraft] = useState<HarnessRoute>(() => routeFromStored(chief.agent || '', chief.model || '', chief.effort || 'low'));
  const harness = harnesses.find(candidate => candidate.id === draft.harness) || defaultHarness;
  const route = draft.harness === harness?.id ? draft : { harness: harness?.id || '', model: '', provider: '', effort: 'low' };
  if (!draft.harness && harness) setDraft(route);
  const view = useHarnessRoute(route, chiefRouteRules(harnesses), 'on-mount');
  const recommended = view.catalog?.models.find(model => model.id === view.catalog?.tier_defaults.deep && model.tier === 'deep' && model.access !== 'unsupported');
  const selected = route.model ? view.selected : recommended;
  const value: HarnessRoute = { ...route, model: selected?.id || route.model, provider: selected?.provider || route.provider, effort: view.effortMode === 'unsupported' ? '' : route.effort };
  const models = view.options().filter(option => option.stored || (option.model && option.model.access !== 'unsupported' && option.model.tier_source !== 'alias'));
  const root = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ top: number; left: number; notch: number } | null>(null);
  const place = useEffectEvent(() => {
    const at = anchor?.getBoundingClientRect();
    const bounds = root.current?.getBoundingClientRect();
    if (!at || !bounds) return;
    const sidebar = anchor?.closest('.sidebar, .queue-sidebar');
    const left = Math.max(0, Math.min((sidebar?.getBoundingClientRect().right ?? at.right) + 10, window.innerWidth - bounds.width));
    const top = Math.max(0, Math.min(at.top - 10, window.innerHeight - bounds.height));
    const notch = Math.max(10, Math.min(at.top + at.height / 2 - top - 5, bounds.height - 20));
    setPosition(previous => previous?.top === top && previous.left === left && previous.notch === notch ? previous : { top, left, notch });
  });
  useLayoutEffect(() => { place(); });
  const outside = useEffectEvent((event: MouseEvent) => {
    if (root.current?.contains(event.target as Node) || anchor?.contains(event.target as Node)) return;
    event.preventDefault();
    onClose();
  });
  useEffect(() => {
    document.addEventListener('mousedown', outside);
    window.addEventListener('resize', place);
    document.addEventListener('scroll', place, true);
    const observer = new ResizeObserver(place);
    if (root.current) observer.observe(root.current);
    if (anchor) observer.observe(anchor);
    return () => {
      document.removeEventListener('mousedown', outside);
      window.removeEventListener('resize', place);
      document.removeEventListener('scroll', place, true);
      observer.disconnect();
    };
  }, [anchor]);
  useEscapeStack(onClose, true);
  const focusRequested = useRef<string | null>('primary');
  const retainFocus = useCallback((element: HTMLButtonElement | null) => {
    if (!element) return;
    return () => { if (document.activeElement === element) focusRequested.current = element.closest('[role="radiogroup"]')?.getAttribute('aria-label') || 'primary'; };
  }, []);
  useLayoutEffect(() => {
    if (!focusRequested.current) return;
    const radio = focusRequested.current !== 'primary' && root.current?.querySelector<HTMLElement>(`[aria-label="${focusRequested.current}"] [aria-checked="true"]:not(:disabled)`);
    const primary = root.current?.querySelector<HTMLElement>('.chief-pop-primary:not([aria-disabled="true"]), .chief-link:not(:disabled)');
    const target = radio || primary;
    if (target) { target.focus({ preventScroll: true }); focusRequested.current = null; }
    else root.current?.focus({ preventScroll: true });
  });
  useLayoutEffect(() => {
    if (!focusRequested.current) return;
    const cancel = () => { focusRequested.current = null; };
    const focusMoved = (event: FocusEvent) => {
      if (event.target !== root.current && event.target !== anchor.closest('.app')) cancel();
    };
    document.addEventListener('focusin', focusMoved, true);
    document.addEventListener('pointerdown', cancel, true);
    return () => {
      document.removeEventListener('focusin', focusMoved, true);
      document.removeEventListener('pointerdown', cancel, true);
    };
  });
  const busy = chief.requestPending;
  const checking = catalog.loading && !catalog.hasCatalog;
  const canSave = !busy && Boolean(harness && value.model && !catalog.loading && !catalog.error && !view.loading && !view.error && (!firstRun || folder.trim()));
  const submit = () => {
    if (!canSave) return;
    const returnFocus = Boolean(!firstRun && root.current?.contains(document.activeElement));
    void onSave(value, firstRun ? folder.trim() : undefined).then(saved => { if (saved && returnFocus) onReturnFocus(); });
  };
  const recheck = () => {
    focusRequested.current = 'primary';
    sendGetSettings();
    catalog.retry();
    view.discover(true);
  };
  const moveRadio = (event: KeyboardEvent) => {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const radio = (event.target as HTMLElement).closest('button[role="radio"]');
    const group = radio?.closest('[role="radiogroup"]');
    if (!group) return;
    if (['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'].includes(event.key)) {
      const buttons = Array.from(group.querySelectorAll<HTMLButtonElement>('button[role="radio"]:not(:disabled)'));
      const current = buttons.indexOf(event.target as HTMLButtonElement);
      const step = event.key === 'ArrowDown' || event.key === 'ArrowRight' ? 1 : -1;
      const next = buttons[(current + step + buttons.length) % buttons.length];
      if (next) { event.preventDefault(); event.stopPropagation(); next.click(); next.focus(); }
    }
    if (event.key === 'Enter') { event.preventDefault(); event.stopPropagation(); submit(); }
  };
  const primaryLabel = chief.phase === 'configuring' ? firstRun ? 'Waking…' : 'Saving…' : firstRun ? `Wake ${chief.name}` : chief.sessionId ? 'Save for next wake' : 'Save';
  return createPortal(<div ref={root} tabIndex={-1} className="chief-pop" data-testid="chief-setup" role="dialog" aria-label={firstRun ? `Set up ${chief.name}` : `${chief.name}'s harness`} style={position ? { top: position.top, left: position.left } : { top: 0, left: 0 }} onKeyDown={moveRadio} onBlur={event => {
    if (event.relatedTarget instanceof Node && !event.currentTarget.contains(event.relatedTarget) && !anchor?.contains(event.relatedTarget)) onClose(false);
  }}>
    <span className="chief-pop-notch" aria-hidden="true" style={{ top: position?.notch }} />
    <header className="chief-pop-head">
      <h3>{firstRun ? `Wake your ${chief.name.toLowerCase()}` : `${chief.name}'s harness`}</h3>
      <button type="button" className="chief-pop-close" aria-label="Close chief setup" onClick={() => onClose()}><kbd>esc</kbd></button>
    </header>
    <div className="chief-pop-body">
    {checking ? <p className="chief-quiet" role="status">Checking harnesses…</p> : !harnesses.length || catalog.error ? <>
      {catalog.error ? <p className="chief-setup-error" role="alert">{catalog.error}</p> : <p className="chief-quiet">No harness here can run the chief. Install Claude Code or Codex to wake it.</p>}
      <div className="chief-link-row">
        <button ref={retainFocus} type="button" className="chief-link" onClick={recheck}>Check again</button>
        {onOpenSettings && <button type="button" className="chief-link" onClick={onOpenSettings}>Settings › Agents</button>}
      </div>
    </> : <>
      {harnesses.length > 1 && <section>
        <span className="chief-pop-key">Harness</span>
        <div className="chief-seg" role="radiogroup" aria-label="Chief harness">
          {harnesses.map(candidate => <button ref={retainFocus} type="button" role="radio" key={candidate.id} disabled={busy} aria-checked={harness?.id === candidate.id} tabIndex={harness?.id === candidate.id ? 0 : -1} onClick={() => setDraft({ harness: candidate.id, model: '', provider: '', effort: 'low' })}>{candidate.name}</button>)}
        </div>
      </section>}
      <section>
        <span className="chief-pop-key">Model</span>
        <div className="chief-pop-models" role="radiogroup" aria-label="Chief model">
          {view.error ? <>
            <p className="chief-setup-error" role="alert">{view.error}</p>
            <button ref={retainFocus} type="button" className="chief-link" onClick={() => { focusRequested.current = 'primary'; view.discover(true); }}>Check again</button>
          </> : models.map(({ model, stored, label }) => {
            const checked = stored || (value.model === model?.id && value.provider === model?.provider);
            return <button ref={retainFocus} type="button" role="radio" key={`${model?.provider ?? value.provider}/${model?.id ?? value.model}`} aria-checked={checked} tabIndex={checked ? 0 : -1} disabled={busy} onClick={() => { if (model) setDraft(view.withModel(model)); }}>
              <span>{label}</span>{model === recommended ? <small>recommended</small> : model?.tier ? <small>{model.tier}</small> : null}
            </button>;
          })}
          {view.loading && <p className="chief-quiet" role="status">Checking models…</p>}
        </div>
      </section>
      {view.effortMode === 'levels' && <section>
        <span className="chief-pop-key">Effort</span>
        <div className="chief-seg" role="radiogroup" aria-label="Chief effort">
          {view.effortLevels.map(effort => <button ref={retainFocus} type="button" role="radio" key={effort} disabled={busy} aria-checked={value.effort === effort} tabIndex={value.effort === effort ? 0 : -1} onClick={() => setDraft({ ...value, effort })}>{effort}</button>)}
        </div>
      </section>}
      {firstRun && <label className="chief-folder">
        <span className="chief-folder-key">Notebook</span>
        <input aria-label="Notebook folder" spellCheck={false} value={folder} title={folder} disabled={busy} onChange={event => setNotebookRoot(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submit(); } }} />
      </label>}
      {chief.setupError && <p className="chief-setup-error" role="alert">{chief.setupError}</p>}
      <footer className="chief-pop-foot">
        <button type="button" className="chief-pop-primary" aria-disabled={!canSave} tabIndex={canSave ? 0 : -1} onClick={submit}>{primaryLabel}<kbd aria-hidden="true">⏎</kbd></button>
      </footer>
    </>}
    </div>
  </div>, document.body);
}
