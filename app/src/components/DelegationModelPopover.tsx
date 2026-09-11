import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { DelegationHarness, DelegationModel, DelegationSelection } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './DelegationModelPopover.css';

export type Anchor = { top: number; bottom: number; left: number; right: number };

// Catalogs live for the app's lifetime; refresh asks the harness again for one harness.
const catalogs = new Map<string, DelegationModelCatalog>();
const inflight = new Map<string, Promise<DelegationModelCatalog>>();
const failures = new Map<string, string>();
export function clearDelegationModelCatalogs() { catalogs.clear(); inflight.clear(); failures.clear(); }
export const knownModelName = (harness: string, provider: string, id: string) => catalogs.get(harness)?.models.find(m => m.id === id && m.provider === provider)?.name || '';

function useCatalog(harness: DelegationHarness | undefined, loadModels: (harness: string) => Promise<DelegationModelCatalog>) {
  const [, rerender] = useState(0);
  const id = harness?.id ?? '';
  const wake = (request: Promise<unknown>) => void request.finally(() => rerender(n => n + 1));
  const discover = (force = false) => {
    if (!id || (!force && catalogs.has(id))) return;
    // A popover reopened while discovery runs waits on the same request instead of starting one.
    const running = inflight.get(id);
    if (running && !force) { wake(running); return; }
    catalogs.delete(id);
    failures.delete(id);
    const request = loadModels(id).then(result => { catalogs.set(id, result); return result; })
      .catch((e: unknown) => { failures.set(id, e instanceof Error ? e.message : String(e)); return { models: [], detail: '' }; })
      .finally(() => { inflight.delete(id); });
    inflight.set(id, request);
    wake(request);
    rerender(n => n + 1);
  };
  useEffect(() => { if (harness?.discovery) discover(); }, [id]); // eslint-disable-line react-hooks/exhaustive-deps
  return { catalog: catalogs.get(id), loading: inflight.has(id), error: failures.get(id) ?? '', discover };
}

const VIEWPORT_MARGIN = 16;

export function DelegationModelPopover({ value, harnesses, anchor, onChange, onClose, loadModels }: {
  value: DelegationSelection;
  harnesses: DelegationHarness[];
  anchor: Anchor;
  onChange: (selection: DelegationSelection) => void;
  onClose: () => void;
  loadModels: (harness: string) => Promise<DelegationModelCatalog>;
}) {
  const harness = harnesses.find(h => h.id === value.harness);
  const { catalog, loading, error, discover } = useCatalog(harness, loadModels);
  const [manual, setManual] = useState(false);
  const [manualID, setManualID] = useState('');
  const [manualProvider, setManualProvider] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState({ top: anchor.bottom + 6, left: anchor.left });
  // A field that commits on blur must commit before the popover unmounts under it.
  const close = () => {
    const focused = document.activeElement;
    if (focused instanceof HTMLElement && containerRef.current?.contains(focused)) focused.blur();
    onClose();
  };
  useEscapeStack(close, true);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    let top = anchor.bottom + 6;
    let left = anchor.left;
    if (left + rect.width > window.innerWidth - VIEWPORT_MARGIN) left = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN);
    if (top + rect.height > window.innerHeight - VIEWPORT_MARGIN) top = Math.max(VIEWPORT_MARGIN, anchor.top - rect.height - 6);
    setPosition({ top, left });
  }, [anchor, loading, manual]);

  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    containerRef.current?.querySelector<HTMLButtonElement>('.delegation-pop-harness[aria-selected="true"], .delegation-pop-harness')?.focus();
    return () => { if (!document.activeElement || document.activeElement === document.body) opener?.focus(); };
  }, []);

  // Deferred a tick so the click that opened the popover doesn't close it.
  useEffect(() => {
    const onMouseDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) close();
    };
    const id = window.setTimeout(() => document.addEventListener('mousedown', onMouseDown), 0);
    return () => { window.clearTimeout(id); document.removeEventListener('mousedown', onMouseDown); };
  }, [onClose]); // eslint-disable-line react-hooks/exhaustive-deps

  const pickHarness = (next: DelegationHarness) => {
    if (next.id === value.harness) return;
    setManual(false);
    onChange({ harness: next.id, provider: '', model: '', effort: '' });
  };
  const pickModel = (model: DelegationModel | null) => {
    setManual(false);
    if (!model) { onChange({ ...value, provider: '', model: '', effort: '' }); return; }
    const effort = model.effort_levels.includes(value.effort) ? value.effort : '';
    onChange({ ...value, provider: model.provider, model: model.id, effort });
  };
  const submitManual = () => {
    const id = manualID.trim();
    if (!id) return;
    onChange({ ...value, provider: pluginHarness ? manualProvider.trim() : '', model: id, effort: '' });
    setManual(false);
    setManualID('');
    setManualProvider('');
  };

  const selected = catalog?.models.find(m => m.id === value.model && m.provider === value.provider);
  const pinned = harness && !harness.model_pin;
  // Built-in harnesses run their configured provider; a plugin harness takes provider/model.
  const pluginHarness = !!harness && !['claude', 'codex', 'copilot'].includes(harness.id);
  const levels = selected?.effort_levels ?? [];
  const showEffort = harness?.effort_pin && value.model !== '' && selected?.effort_support !== 'unsupported';

  // Portal: the settings modal is transformed, which would make a fixed popover position against it.
  return createPortal(<div ref={containerRef} className="delegation-pop" role="dialog" aria-label="Choose a model" style={{ top: position.top, left: position.left }}>
    <div className="delegation-pop-harnesses" role="listbox" aria-label="Harness">
      <div className="delegation-pop-kicker">Harness</div>
      {harnesses.map(h => <button key={h.id} type="button" role="option" className="delegation-pop-harness" data-harness={h.id} aria-selected={h.id === value.harness} onClick={() => pickHarness(h)}>
        <span>{h.name}</span>
        <span className={`delegation-pop-status ${h.available ? '' : 'bad'}`} title={h.available ? 'Available' : 'Not installed on this daemon'} />
      </button>)}
      {value.harness && !harness && <button type="button" role="option" className="delegation-pop-harness" aria-selected>{value.harness} (unknown)</button>}
    </div>
    <div className="delegation-pop-models">
      {!harness && <p className="delegation-pop-note">Pick a harness to see its models.</p>}
      {pinned && <>
        <div className="delegation-pop-kicker">Model</div>
        <p className="delegation-pop-note">{harness.name} uses the model selected in its own settings. Attn can't pin one here.</p>
      </>}
      {harness && !pinned && <>
        <div className="delegation-pop-kicker"><span>Model</span>{harness.discovery && <button type="button" className="delegation-pop-refresh" disabled={loading} onClick={() => discover(true)}>refresh</button>}</div>
        <div role="listbox" aria-label="Model" className="delegation-pop-list">
          <button type="button" role="option" className="delegation-pop-model default" data-model="" aria-selected={value.model === ''} onClick={() => pickModel(null)}>
            <span><span className="delegation-pop-model-name">{harness.name} default</span><span className="delegation-pop-model-detail">Whatever the harness would pick on its own.</span></span>
          </button>
          {loading && <div className="delegation-pop-skeleton" aria-label="Discovering models"><i style={{ width: '55%' }} /><i style={{ width: '70%' }} /><i style={{ width: '40%' }} /></div>}
          {catalog?.models.map(m => <button key={`${m.provider}/${m.id}`} type="button" role="option" className="delegation-pop-model" data-model={m.id} aria-selected={selected === m} disabled={m.access === 'unsupported'} onClick={() => pickModel(m)}>
            <span><span className="delegation-pop-model-name">{m.provider ? `${m.provider} / ` : ''}{m.name || m.id}</span>{(m.description || m.detail) && <span className="delegation-pop-model-detail">{m.description || m.detail}</span>}</span>
          </button>)}
          {value.model && !selected && !loading && <button type="button" role="option" className="delegation-pop-model" aria-selected>
            <span><span className="delegation-pop-model-name">{value.provider ? `${value.provider} / ` : ''}{value.model}</span><span className="delegation-pop-model-detail">Entered by hand.</span></span>
          </button>}
        </div>
        {error && <p className="delegation-pop-note warn" role="alert">{error}</p>}
        {showEffort && <div className="delegation-pop-effort">
          <span className="delegation-pop-kicker">Effort</span>
          {levels.length > 0
            ? <span className="delegation-pop-seg" role="group" aria-label="Effort">
              {['', ...levels].map(level => <button key={level} type="button" data-effort={level} aria-pressed={value.effort === level} onClick={() => onChange({ ...value, effort: level })}>{level || 'default'}</button>)}
            </span>
            : <input aria-label="Effort" className="delegation-pop-input" placeholder="as the harness names it" defaultValue={value.effort} onBlur={e => { if (e.target.value.trim() !== value.effort) onChange({ ...value, effort: e.target.value.trim() }); }} onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur(); }} />}
        </div>}
        {manual
          ? <form className="delegation-pop-manual" onSubmit={e => { e.preventDefault(); submitManual(); }}>
            {pluginHarness && <input aria-label="Provider" className="delegation-pop-input" autoFocus value={manualProvider} placeholder="Provider, as the harness names it" onChange={e => setManualProvider(e.target.value)} />}
            <input aria-label="Model ID" className="delegation-pop-input" autoFocus={!pluginHarness} value={manualID} placeholder="Exact model ID from the harness" onChange={e => setManualID(e.target.value)} />
            <button type="submit" className="settings-action" disabled={!manualID.trim()}>Use it</button>
          </form>
          : null}
      </>}
    </div>
    <div className="delegation-pop-foot">
      <span>{harness && !pinned ? (loading ? `Asking ${harness.name} which models it knows…` : catalog?.detail || (harness.discovery ? `Models ${harness.name} reports. Being listed doesn't prove your account can use one.` : 'This harness does not list models. Enter an exact ID or use its default.')) : pinned ? 'Choosing this harness saves it as the route.' : ''}</span>
      {harness && !pinned && !manual && <button type="button" className="delegation-pop-link" onClick={() => setManual(true)}>Enter a model ID</button>}
    </div>
  </div>, document.body);
}
