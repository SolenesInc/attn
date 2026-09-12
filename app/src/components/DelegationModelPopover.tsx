import { useEffect, useEffectEvent, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { DelegationHarness, DelegationModel, DelegationSelection } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import { useDelegationModelCatalog } from '../hooks/useDelegationModelCatalog';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './DelegationModelPopover.css';

export type Anchor = Element;

const VIEWPORT_MARGIN = 16;
// Built-in harnesses run their configured provider; a plugin harness takes provider/model.
const isPluginHarness = (harness: DelegationHarness) => !['claude', 'codex', 'copilot'].includes(harness.id);

function HarnessList({ harnesses, value, onPick, onClear }: { harnesses: DelegationHarness[]; value: string; onPick: (harness: DelegationHarness) => void; onClear: () => void }) {
  const known = harnesses.some(h => h.id === value);
  return <div className="delegation-pop-harnesses" role="listbox" aria-label="Harness">
    <div className="delegation-pop-kicker">Harness</div>
    <button type="button" role="option" className="delegation-pop-harness none" data-harness="" aria-selected={value === ''} onClick={onClear}>None</button>
    {harnesses.map(h => <button key={h.id} type="button" role="option" className="delegation-pop-harness" data-harness={h.id} aria-selected={h.id === value} onClick={() => onPick(h)}>
      <span>{h.name}</span>
      <span className={`delegation-pop-status ${h.available ? '' : 'bad'}`} title={h.available ? 'Available' : 'Not installed on this daemon'} />
    </button>)}
    {value && !known && <button type="button" role="option" className="delegation-pop-harness" aria-selected>{value} (unknown)</button>}
  </div>;
}

function ModelList({ harness, catalog, loading, value, selected, onPick }: { harness: DelegationHarness; catalog: DelegationModelCatalog | undefined; loading: boolean; value: DelegationSelection; selected: DelegationModel | undefined; onPick: (model: DelegationModel | null) => void }) {
  const entered = value.model && !selected && !loading;
  return <div role="listbox" aria-label="Model" className="delegation-pop-list">
    <button type="button" role="option" className="delegation-pop-model default" data-model="" aria-selected={value.model === ''} onClick={() => onPick(null)}>
      <span><span className="delegation-pop-model-name">{harness.name} default</span><span className="delegation-pop-model-detail">Whatever the harness would pick on its own.</span></span>
    </button>
    {loading && <div className="delegation-pop-skeleton" aria-label="Discovering models"><i style={{ width: '55%' }} /><i style={{ width: '70%' }} /><i style={{ width: '40%' }} /></div>}
    {catalog?.models.map(m => <button key={`${m.provider}/${m.id}`} type="button" role="option" className="delegation-pop-model" data-model={m.id} aria-selected={selected === m} disabled={m.access === 'unsupported'} onClick={() => onPick(m)}>
      <span><span className="delegation-pop-model-name">{m.provider ? `${m.provider} / ` : ''}{m.name || m.id}</span>{(m.description || m.detail) && <span className="delegation-pop-model-detail">{m.description || m.detail}</span>}</span>
    </button>)}
    {entered && <button type="button" role="option" className="delegation-pop-model" aria-selected>
      <span><span className="delegation-pop-model-name">{value.provider ? `${value.provider} / ` : ''}{value.model}</span><span className="delegation-pop-model-detail">Entered by hand.</span></span>
    </button>}
  </div>;
}

const routeKey = (value: DelegationSelection) => `${value.harness}/${value.provider}/${value.model}`;

function EffortField({ levels, value, onChange }: { levels: string[]; value: string; onChange: (effort: string) => void }) {
  return <div className="delegation-pop-effort">
    <span className="delegation-pop-kicker">Effort</span>
    {levels.length > 0
      ? <span className="delegation-pop-seg" role="group" aria-label="Effort">
        {['', ...levels].map(level => <button key={level} type="button" data-effort={level} aria-pressed={value === level} onClick={() => onChange(level)}>{level || 'default'}</button>)}
      </span>
      : <input aria-label="Effort" className="delegation-pop-input" placeholder="as the harness names it" defaultValue={value} onBlur={e => { if (e.target.value.trim() !== value) onChange(e.target.value.trim()); }} onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur(); }} />}
  </div>;
}

function ManualEntry({ withProvider, onSubmit }: { withProvider: boolean; onSubmit: (model: string, provider: string) => void }) {
  const [model, setModel] = useState('');
  const [provider, setProvider] = useState('');
  return <form className="delegation-pop-manual" onSubmit={e => { e.preventDefault(); if (model.trim()) onSubmit(model.trim(), withProvider ? provider.trim() : ''); }}>
    {withProvider && <input aria-label="Provider" className="delegation-pop-input" autoFocus value={provider} placeholder="Provider, as the harness names it" onChange={e => setProvider(e.target.value)} />}
    <input aria-label="Model ID" className="delegation-pop-input" autoFocus={!withProvider} value={model} placeholder="Exact model ID from the harness" onChange={e => setModel(e.target.value)} />
    <button type="submit" className="settings-action" disabled={!model.trim()}>Use it</button>
  </form>;
}

function footText(harness: DelegationHarness | undefined, loading: boolean, catalog: DelegationModelCatalog | undefined) {
  if (!harness) return '';
  if (!harness.model_pin) return 'Choosing this harness saves it as the route.';
  if (loading) return `Asking ${harness.name} which models it knows…`;
  if (catalog?.detail) return catalog.detail;
  return harness.discovery ? `Models ${harness.name} reports. Being listed doesn't prove your account can use one.` : 'This harness does not list models. Enter an exact ID or use its default.';
}

function ModelsPane({ harness, value, catalog, loading, error, manual, discover, onChange, onDone }: {
  harness: DelegationHarness;
  value: DelegationSelection;
  catalog: DelegationModelCatalog | undefined;
  loading: boolean;
  error: string;
  manual: boolean;
  discover: (force: boolean) => void;
  onChange: (selection: DelegationSelection) => void;
  onDone: () => void;
}) {
  const selected = catalog?.models.find(m => m.id === value.model && m.provider === value.provider);
  const showEffort = harness.effort_pin && selected?.effort_support !== 'unsupported';
  const pickModel = (model: DelegationModel | null) => {
    onDone();
    if (!model) { onChange({ ...value, provider: '', model: '', effort: '' }); return; }
    const effort = model.effort_levels.includes(value.effort) ? value.effort : '';
    onChange({ ...value, provider: model.provider, model: model.id, effort });
  };
  return <>
    <div className="delegation-pop-kicker"><span>Model</span>{harness.discovery && <button type="button" className="delegation-pop-refresh" disabled={loading} onClick={() => discover(true)}>refresh</button>}</div>
    <ModelList harness={harness} catalog={catalog} loading={loading} value={value} selected={selected} onPick={pickModel} />
    {error && <p className="delegation-pop-note warn" role="alert">{error}</p>}
    {showEffort && <EffortField key={routeKey(value)} levels={selected?.effort_levels ?? []} value={value.effort} onChange={effort => onChange({ ...value, effort })} />}
    {manual && <ManualEntry withProvider={isPluginHarness(harness)} onSubmit={(model, provider) => { onChange({ ...value, provider, model, effort: '' }); onDone(); }} />}
  </>;
}

export function DelegationModelPopover({ value, harnesses, anchor, onChange, onClose, loadModels }: {
  value: DelegationSelection;
  harnesses: DelegationHarness[];
  anchor: Anchor;
  onChange: (selection: DelegationSelection) => void;
  onClose: () => void;
  loadModels: (harness: string) => Promise<DelegationModelCatalog>;
}) {
  const harness = harnesses.find(h => h.id === value.harness);
  const { catalog, loading, error, discover } = useDelegationModelCatalog(harness, loadModels);
  const [manual, setManual] = useState(false);
  const containerRef = useRef<HTMLDialogElement>(null);
  const [position, setPosition] = useState(() => ({ top: anchor.getBoundingClientRect().bottom + 6, left: anchor.getBoundingClientRect().left }));
  // A field that commits on blur must commit before the popover unmounts under it.
  const close = () => {
    const focused = document.activeElement;
    if (focused instanceof HTMLElement && containerRef.current?.contains(focused)) focused.blur();
    onClose();
  };
  useEscapeStack(close, true);

  useLayoutEffect(() => {
    const place = () => {
      const el = containerRef.current;
      if (!el) return;
      const rect = el.getBoundingClientRect();
      const at = anchor.getBoundingClientRect();
      let top = at.bottom + 6;
      let left = at.left;
      if (left + rect.width > window.innerWidth - VIEWPORT_MARGIN) left = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN);
      if (top + rect.height > window.innerHeight - VIEWPORT_MARGIN) top = Math.max(VIEWPORT_MARGIN, at.top - rect.height - 6);
      setPosition(current => current.top === top && current.left === left ? current : { top, left });
    };
    place();
    window.addEventListener('resize', place);
    document.addEventListener('scroll', place, true);
    return () => { window.removeEventListener('resize', place); document.removeEventListener('scroll', place, true); };
  }, [anchor, loading, manual]);

  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const list = containerRef.current;
    (list?.querySelector<HTMLButtonElement>('.delegation-pop-harness[aria-selected="true"]') ?? list?.querySelector<HTMLButtonElement>('.delegation-pop-harness'))?.focus();
    return () => { if (!document.activeElement || document.activeElement === document.body) opener?.focus(); };
  }, []);

  const onMouseDown = useEffectEvent((event: MouseEvent) => {
    if (containerRef.current && !containerRef.current.contains(event.target as Node)) close();
  });
  // Deferred a tick so the click that opened the popover doesn't close it.
  useEffect(() => {
    const id = window.setTimeout(() => document.addEventListener('mousedown', onMouseDown), 0);
    return () => { window.clearTimeout(id); document.removeEventListener('mousedown', onMouseDown); };
  }, []);

  const pickHarness = (next: DelegationHarness) => {
    if (next.id === value.harness) return;
    setManual(false);
    onChange({ harness: next.id, provider: '', model: '', effort: '' });
  };
  const clearHarness = () => {
    if (value.harness) onChange({ harness: '', provider: '', model: '', effort: '' });
    close();
  };
  const pinnable = harness?.model_pin ?? false;

  // Portal: the settings modal is transformed, which would make a fixed popover position against it.
  const onFocusOut = (event: React.FocusEvent<HTMLDialogElement>) => {
    const dialog = event.currentTarget;
    const next = event.relatedTarget;
    if (next instanceof Node) { if (!dialog.contains(next)) onClose(); return; }
    window.setTimeout(() => {
      if (!dialog.isConnected || dialog.contains(document.activeElement)) return;
      (dialog.querySelector<HTMLElement>('.delegation-pop-harness[aria-selected="true"]') ?? dialog.querySelector<HTMLElement>('button, input'))?.focus();
    }, 0);
  };
  return createPortal(<dialog open ref={containerRef} className="delegation-pop" aria-label="Choose a model" style={{ top: position.top, left: position.left }} onBlur={onFocusOut}>
    <HarnessList harnesses={harnesses} value={value.harness} onPick={pickHarness} onClear={clearHarness} />
    <div className="delegation-pop-models">
      {!harness && <p className="delegation-pop-note">Pick a harness to see its models. None leaves this route unset.</p>}
      {harness && !pinnable && <>
        <div className="delegation-pop-kicker">Model</div>
        <p className="delegation-pop-note">{harness.name} uses the model selected in its own settings. Attn can't pin one here.</p>
        {harness.effort_pin && <EffortField key={routeKey(value)} levels={[]} value={value.effort} onChange={effort => onChange({ ...value, effort })} />}
      </>}
      {harness && pinnable && <ModelsPane harness={harness} value={value} catalog={catalog} loading={loading} error={error} manual={manual} discover={discover} onChange={onChange} onDone={() => setManual(false)} />}
    </div>
    <div className="delegation-pop-foot">
      <span>{footText(harness, loading, catalog)}</span>
      {pinnable && !manual && <button type="button" className="delegation-pop-link" onClick={() => setManual(true)}>Enter a model ID</button>}
    </div>
  </dialog>, document.body);
}
