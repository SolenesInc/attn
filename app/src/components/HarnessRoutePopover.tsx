import FocusTrap from './AppFocusTrap';
import { Fragment, useEffect, useEffectEvent, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useHarnessRoute, tierLabel, type HarnessRoute, type HarnessRouteRules, type HarnessRouteView } from '../hooks/useHarnessRoute';
import { TierMark } from './TierMark';
import './HarnessRoute.css';

function ModelList({ view, query, onChange }: { view: HarnessRouteView; query: string; onChange: (route: HarnessRoute) => void }) {
  const options = view.options(query);
  let previous = '';
  return <div role="listbox" aria-label="Model" className="route-model-list">
    {(!query || !view.canPinModel) && <button type="button" role="option" className="route-model-option is-default" disabled={view.pinsUnavailable} aria-selected={!view.value.model} data-model="" onClick={() => onChange(view.withModel(undefined))}>{view.unsetLabel}</button>}
    {options.map(option => {
      const header = option.group && option.group !== previous ? option.group === 'no tier' ? 'No tier' : `${tierLabel(option.group)}${option.group === view.rules.tier ? ' recommended' : ''}` : '';
      previous = option.group;
      const model = option.model;
      const selected = model ? model.id === view.value.model && model.provider === view.value.provider : true;
      return <Fragment key={model ? `${model.provider}/${model.id}` : 'stored'}>
        {header && <div className="route-group" role="presentation">{header}</div>}
        <button type="button" role="option" className="route-model-option" aria-selected={selected} data-model={model?.id || view.value.model} disabled={model?.access === 'unsupported'} onClick={() => onChange(option.stored ? view.value : view.withModel(model))}>
          <span className="route-option-title">{option.label}<TierMark row tier={model ? { tier: model.tier, source: model.tier_source } : view.tier || { source: 'none' }} /></span>
          <span className="route-detail">{model ? [model.provider, model.name !== model.id ? model.id : '', model.tier_source === 'alias' ? 'The harness decides' : model.detail || model.description].filter(Boolean).join(' · ') : view.loading ? 'Stored. Checking the catalog…' : 'Not in the catalog. Kept as stored.'}</span>
        </button>
      </Fragment>;
    })}
    {view.loading && <div className="route-loading" role="status">Discovering models…</div>}
  </div>;
}

function EffortControl({ view, onChange, onDone }: { view: HarnessRouteView; onChange: (route: HarnessRoute) => void; onDone: () => void }) {
  const { value } = view;
  const [draft, setDraft] = useState<string | null>(null);
  if (view.effortMode === 'hidden') return null;
  if (view.effortMode === 'pending' && !view.pinsUnavailable) return <div className="route-effort-control"><span className="route-label">Effort</span><span className="route-detail">{value.effort || 'Waiting for the catalog…'}</span></div>;
  if (view.effortMode === 'unsupported' && !value.effort && draft === null) return <div className="route-effort-control"><span className="route-label">Effort</span><span className="route-detail">Not available for this model.</span></div>;
  const levels = view.effortMode === 'levels' && draft === null ? ['', ...view.effortLevels, ...(view.effortMissing ? [value.effort] : [])] : [];
  return <div className="route-effort-control">
    <span className="route-label">Effort</span>
    {levels.length ? <div className="route-effort-options" role="group" aria-label="Effort">{levels.map(level => <button type="button" key={level} data-effort={level} disabled={view.pinsUnavailable} aria-pressed={value.effort === level} className={level === value.effort && view.effortMissing ? 'route-missing' : ''} onClick={() => { onChange({ ...value, effort: level }); onDone(); }}>{level || (view.defaultEffort ? `default (${view.defaultEffort})` : 'default')}</button>)}</div>
      : <input aria-label="Effort" className="settings-input" value={draft ?? value.effort} readOnly={view.pinsUnavailable} placeholder="As the harness names it" onFocus={() => setDraft(value.effort)} onChange={event => setDraft(event.target.value)} onBlur={event => { const effort = event.target.value.trim(); setDraft(null); if (effort !== value.effort) onChange({ ...value, effort }); }} onKeyDown={event => { if (event.key === 'Enter') { event.currentTarget.blur(); onDone(); } }} />}

  </div>;
}

export function HarnessRoutePopover({ value, rules, anchor, onChange, onClose }: { value: HarnessRoute; rules: HarnessRouteRules; anchor: Element; onChange: (route: HarnessRoute) => void; onClose: () => void }) {
  const [previewOverride, setPreview] = useState<string | null>(null);
  const preview = previewOverride ?? (value.harness || (rules.allowNone ? '' : rules.harnesses[0]?.id || ''));
  const [query, setQuery] = useState('');
  const [provider, setProvider] = useState(value.provider);
  const shown = preview === value.harness ? value : { harness: preview, provider: '', model: '', effort: '' };
  const view = useHarnessRoute(shown, rules, 'on-mount');
  const dialog = useRef<HTMLDialogElement>(null);
  const filter = useRef<HTMLInputElement>(null);
  const firstControl = () => filter.current || dialog.current?.querySelector<HTMLElement>('button:not(:disabled), input:not(:disabled)') || dialog.current;
  const restoreFocus = useRef(false);
  const toEffort = useRef(false);
  const [position, setPosition] = useState(() => ({ top: anchor.getBoundingClientRect().bottom, left: anchor.getBoundingClientRect().left }));
  const close = () => {
    const focused = document.activeElement;
    if (focused instanceof HTMLElement && dialog.current?.contains(focused)) focused.blur();
    onClose();
  };
  useEscapeStack(() => { restoreFocus.current = true; close(); }, true);
  const place = useEffectEvent(() => {
    const at = anchor.getBoundingClientRect(), bounds = dialog.current?.getBoundingClientRect();
    if (!bounds) return;
    const left = Math.max(0, Math.min(at.left, window.innerWidth - bounds.width));
    const top = at.bottom + bounds.height <= window.innerHeight ? at.bottom : Math.max(0, at.top - bounds.height);
    setPosition(previous => previous.top === top && previous.left === left ? previous : { top, left });
  });
  useLayoutEffect(() => { place(); });
  const outside = useEffectEvent((event: MouseEvent) => { if (!dialog.current?.contains(event.target as Node) && !anchor.contains(event.target as Node)) close(); });
  useEffect(() => {
    document.addEventListener('mousedown', outside);
    window.addEventListener('resize', place);
    document.addEventListener('scroll', place, true);
    return () => { document.removeEventListener('mousedown', outside); window.removeEventListener('resize', place); document.removeEventListener('scroll', place, true); };
  }, []);
  useLayoutEffect(() => {
    if (!dialog.current?.contains(document.activeElement)) firstControl()?.focus();
  });
  const finish = () => { restoreFocus.current = true; close(); };
  const focusEffort = () => {
    if (view.effortMode === 'hidden' || view.effortMode === 'unsupported') { finish(); return; }
    dialog.current?.querySelector<HTMLElement>('.route-effort-options button, .route-effort-control input')?.focus();
  };
  useLayoutEffect(() => { if (toEffort.current) { toEffort.current = false; focusEffort(); } });
  const pickModel = (next: HarnessRoute) => {
    if (next.harness === value.harness && next.model === value.model && next.provider === value.provider && next.effort === value.effort) { focusEffort(); return; }
    toEffort.current = true;
    setPreview(null);
    setQuery('');
    onChange(next);
  };
  const pickHarness = (id: string) => { setPreview(id); setQuery(''); setProvider(''); };
  const hasProvider = !['claude', 'codex', 'copilot'].includes(view.effectiveHarness);
  let manualModel = query.trim(), manualProvider = hasProvider ? provider.trim() : '';
  if (hasProvider && !manualProvider && manualModel.includes('/')) {
    const slash = manualModel.indexOf('/'); manualProvider = manualModel.slice(0, slash); manualModel = manualModel.slice(slash + 1);
  }
  const manualUnsupported = view.catalog?.models.some(model => model.id === manualModel && model.provider === manualProvider && model.access === 'unsupported');
  const manual = () => {
    if (!manualModel || manualUnsupported) return;
    pickModel({ ...shown, model: manualModel, provider: manualProvider, effort: '' });
  };
  const move = (event: KeyboardEvent<HTMLDialogElement>) => {
    const current = document.activeElement;
    if (!(current instanceof HTMLElement) || !dialog.current) return;
    const zone = current.closest<HTMLElement>('[role="listbox"], .route-effort-options');
    if (zone && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      const items = [...zone.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')];
      const index = items.indexOf(current as HTMLButtonElement);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : Math.max(0, Math.min(items.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
      if (items[next]) { event.preventDefault(); items[next].focus(); }
    } else if (current === filter.current && event.key === 'ArrowDown') {
      event.preventDefault(); dialog.current.querySelector<HTMLButtonElement>('.route-model-list button:not(:disabled)')?.focus();
    } else if (current === filter.current && event.key === 'Enter' && query.trim()) { event.preventDefault(); if (manualUnsupported) return; if (hasProvider && provider.trim()) { manual(); return; } const first = view.options(query).find(option => option.model?.access !== 'unsupported' && !option.stored); if (first?.model) pickModel(view.withModel(first.model)); else manual(); }
    else if ((event.key === 'ArrowLeft' || event.key === 'ArrowRight') && current.tagName !== 'INPUT') {
      const harnessZone = current.closest('.route-harness-list');
      const effortZone = current.closest('.route-effort-control');
      const selector = event.key === 'ArrowLeft'
        ? effortZone ? '.route-model-list button[aria-selected="true"]:not(:disabled), .route-model-list button:not(:disabled)' : '.route-harness-list button[aria-selected="true"]:not(:disabled), .route-harness-list button:not(:disabled)'
        : harnessZone ? '.route-model-list button[aria-selected="true"]:not(:disabled), .route-model-list button:not(:disabled)' : '.route-effort-options button, .route-effort-control input';
      event.preventDefault(); dialog.current.querySelector<HTMLElement>(selector)?.focus();
    }
  };
  return createPortal(<FocusTrap focusTrapOptions={{ escapeDeactivates: false, allowOutsideClick: true, returnFocusOnDeactivate: false, delayInitialFocus: false, initialFocus: () => firstControl() || false, fallbackFocus: () => dialog.current!, onPostDeactivate: () => { if (restoreFocus.current && anchor instanceof HTMLElement && anchor.isConnected) anchor.focus(); } }}><dialog open tabIndex={-1} ref={dialog} className={`route-popover ${rules.fixedHarness ? 'fixed-harness' : ''}`} aria-label="Choose a model" style={position} onKeyDown={move} onBlur={event => { if (event.relatedTarget instanceof Node && !event.currentTarget.contains(event.relatedTarget) && !anchor.contains(event.relatedTarget)) onClose(); }}>
    {!rules.fixedHarness && <div className="route-harness-list" role="listbox" aria-label="Harness">
      <div className="route-kicker">Harness</div>
      {rules.allowNone && <button type="button" role="option" aria-selected={!preview} data-harness="" onClick={() => { onChange({ harness: '', provider: '', model: '', effort: '' }); finish(); }}>{rules.noneLabel || 'None'}</button>}
      {rules.harnesses.map(harness => <button type="button" role="option" key={harness.id} data-harness={harness.id} disabled={Boolean(rules.requireAvailable && !harness.available)} aria-selected={preview === harness.id} onClick={() => pickHarness(harness.id)}><span>{harness.name}</span>{!harness.available && <span className="route-unavailable" title="Unavailable on this daemon">●</span>}</button>)}
      {preview && !rules.harnesses.some(harness => harness.id === preview) && <button type="button" role="option" aria-selected disabled>{preview} (not offered here)</button>}
    </div>}
    <div className="route-popover-main">
      <div className="route-kicker"><span>Model</span>{view.harness && view.harness.discovery !== false && <button type="button" onClick={() => view.discover(true)} disabled={view.loading}>Refresh</button>}</div>
      {view.effectiveHarness ? <>
        {view.canPinModel && <input ref={filter} className="settings-input route-filter" aria-label="Filter models or enter an ID" placeholder="Filter or enter a model ID" value={query} onChange={event => setQuery(event.target.value)} />}
        <ModelList view={view} query={query} onChange={pickModel} />
        {view.canPinModel && query.trim() && <div className="route-manual">{hasProvider && <input className="settings-input" aria-label="Provider" placeholder="Provider" value={provider} onChange={event => setProvider(event.target.value)} />}<button type="button" className="settings-action" disabled={manualUnsupported} onClick={manual}>Use “{query.trim()}”</button></div>}
        <EffortControl view={view} onChange={onChange} onDone={finish} />
        {view.catalog?.detail && <p className="route-detail">{view.catalog.detail}</p>}
        {view.harness?.discovery === false && <p className="route-detail">{view.canPinModel ? 'Enter an exact model ID or use the harness default.' : 'The harness chooses its model.'}</p>}
        {view.error && <div className="settings-warning" role="alert">{view.error}</div>}
      </> : <p className="route-detail">Choose a harness to see its models.</p>}
    </div>
  </dialog></FocusTrap>, document.body);
}
