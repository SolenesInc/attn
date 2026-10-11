import { useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useHarnesses } from '../hooks/useHarnesses';
import { routeFromStored, useHarnessRoute, type HarnessRoute } from '../hooks/useHarnessRoute';
import type { ChiefView } from '../application/useChief';
import { chiefHarnesses, chiefRouteRules } from './chiefRoute';

export function ChiefSetup({ chief, autoFocus, onSave, onClose, onReturnFocus, onOpenSettings }: {
  chief: ChiefView;
  autoFocus: boolean;
  onSave: (route: HarnessRoute, restart?: boolean) => Promise<boolean>;
  onClose: () => void;
  onReturnFocus: () => void;
  onOpenSettings?: () => void;
}) {
  const { settings, sendGetSettings } = useDaemonApi();
  const catalog = useHarnesses();
  const harnesses = chiefHarnesses(catalog.harnesses, settings);
  const defaultHarness = harnesses.find(harness => harness.id === settings.new_session_agent) || harnesses[0];
  const [draft, setDraft] = useState<HarnessRoute>(() => routeFromStored(chief.agent || '', chief.model || '', chief.effort || 'low'));
  const [step, setStep] = useState<'harness' | 'model'>(chief.agent ? 'model' : 'harness');
  const harness = harnesses.find(candidate => candidate.id === draft.harness) || defaultHarness;
  const route = draft.harness === harness?.id ? draft : { harness: harness?.id || '', model: '', provider: '', effort: 'low' };
  const view = useHarnessRoute(route, chiefRouteRules(harnesses), 'on-mount');
  const recommended = view.catalog?.models.find(model => model.id === view.catalog?.tier_defaults.deep && model.tier === 'deep' && model.access !== 'unsupported');
  const selected = route.model ? view.selected : recommended;
  const value: HarnessRoute = { ...route, model: selected?.id || route.model, provider: selected?.provider || route.provider, effort: view.effortMode === 'unsupported' ? '' : route.effort };
  const models = view.options().filter(option => option.stored || (option.model && option.model.access !== 'unsupported'));
  const showHarness = step === 'harness' && harnesses.length > 1;
  const root = useRef<HTMLDivElement>(null);
  const focusRequested = useRef(autoFocus);
  useLayoutEffect(() => { focusRequested.current = autoFocus; }, [autoFocus]);
  const changeStep = (next: 'harness' | 'model') => { focusRequested.current = true; setStep(next); };
  useLayoutEffect(() => {
    if (!focusRequested.current && !root.current?.contains(document.activeElement)) return;
    const selected = root.current?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]:not(:disabled)');
    const first = root.current?.querySelector<HTMLElement>('[role="radio"]:not(:disabled), .chief-setup-primary:not(:disabled), .chief-setup-buttons button:not(:disabled)');
    const target = selected || first;
    if (target) { target.focus(); focusRequested.current = false; }
  }, [autoFocus, step, showHarness, harnesses.length, view.catalog]);
  const busy = chief.requestPending;
  const canSave = !busy && Boolean(harness && value.model && !view.loading && !view.error);
  const submit = (restart = false) => {
    if (!canSave) return;
    const returnFocus = Boolean(chief.agent && root.current?.contains(document.activeElement));
    void onSave(value, restart).then(saved => { if (saved && returnFocus) onReturnFocus(); });
  };
  const moveRadio = (event: KeyboardEvent, kind: 'harness' | 'model' | 'effort') => {
    const group = event.currentTarget;
    const buttons = Array.from(group.querySelectorAll<HTMLButtonElement>('button[role="radio"]:not(:disabled)'));
    const current = buttons.findIndex(button => button.getAttribute('aria-checked') === 'true');
    const forward = kind === 'effort' ? 'ArrowRight' : 'ArrowDown';
    const backward = kind === 'effort' ? 'ArrowLeft' : 'ArrowUp';
    let next = event.key === forward ? (current + 1) % buttons.length : event.key === backward ? (current - 1 + buttons.length) % buttons.length : -1;
    if (kind !== 'effort' && /^[1-9]$/.test(event.key)) next = Number(event.key) - 1;
    if (next >= 0 && buttons[next]) { event.preventDefault(); buttons[next].click(); buttons[next].focus(); }
    if (event.key === 'Enter') {
      event.preventDefault();
      if (kind === 'harness') changeStep('model');
      else if (kind === 'model') submit();
    }
  };
  return <div ref={root} className="chief-setup" data-testid="chief-setup" onKeyDown={event => {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); }
  }}>
    <div className="chief-setup-heading"><h3>{chief.agent ? 'Harness and model' : 'Set up your chief'}</h3><button type="button" aria-label="Close chief setup" onClick={onClose}>×</button></div>
    {catalog.loading && !catalog.hasCatalog ? <p role="status">Checking harnesses…</p> : !harnesses.length ? <>
      <p>No harness here can run the chief</p>
      <div className="chief-install-row">Claude Code <span>Not installed</span></div>
      <div className="chief-install-row">Codex <span>Not installed</span></div>
      <div className="chief-setup-buttons">
        {onOpenSettings && <button type="button" onClick={onOpenSettings}>Settings › Agents</button>}
        <button type="button" onClick={() => { sendGetSettings(); catalog.retry(); }}>Check again</button>
      </div>
    </> : showHarness ? <>
      <div role="radiogroup" aria-label="Chief harness" onKeyDown={event => moveRadio(event, 'harness')}>
        {harnesses.map((candidate, index) => <button type="button" role="radio" aria-checked={harness?.id === candidate.id} tabIndex={harness?.id === candidate.id ? 0 : -1} key={candidate.id} onClick={() => setDraft({ harness: candidate.id, model: '', provider: '', effort: 'low' })}>
          <span className="chief-radio-mark">{harness?.id === candidate.id ? '●' : '○'}</span><span>{candidate.name}</span><kbd>{index + 1}</kbd>
        </button>)}
      </div>
      <button type="button" className="chief-setup-primary" onClick={() => changeStep('model')}>Continue <kbd>⏎</kbd></button>
    </> : <>
      <div className="chief-chosen-harness"><span>{harness.name}</span>{harnesses.length > 1 ? <button type="button" onClick={() => changeStep('harness')}>change</button> : <small>the only one installed</small>}</div>
      <div role="radiogroup" aria-label="Chief model" onKeyDown={event => moveRadio(event, 'model')}>
        {models.map(({ model, stored, label }, index) => {
          const checked = stored || (value.model === model?.id && value.provider === model?.provider);
          return <button type="button" role="radio" key={stored ? 'stored' : `${model?.provider}/${model?.id}`} aria-checked={checked} tabIndex={checked ? 0 : -1} disabled={busy} onClick={() => { if (model) setDraft(view.withModel(model)); }}>
            <span className="chief-radio-mark">{checked ? '●' : '○'}</span>
            <span>{label}{model === recommended && <small>recommended</small>}</span>
            <kbd>{index + 1}</kbd>
          </button>;
        })}
      </div>
      {view.loading && <p role="status">Checking models…</p>}
      {view.error && <div role="alert">{view.error}<button type="button" onClick={() => view.discover(true)}>Check again</button></div>}
      {view.effortMode === 'levels' && <div className="chief-effort" role="radiogroup" aria-label="Chief effort" onKeyDown={event => moveRadio(event, 'effort')}>
        {view.effortLevels.map(effort => <button type="button" role="radio" key={effort} disabled={busy} aria-checked={value.effort === effort} tabIndex={value.effort === effort ? 0 : -1} onClick={() => setDraft({ ...value, effort })}>{effort}</button>)}
      </div>}
      {chief.agent && chief.sessionId ? <div className="chief-setup-buttons is-stacked">
        <button type="button" className="chief-setup-primary" disabled={!canSave} onClick={() => submit(true)}>Save and start a new day</button>
        <button type="button" disabled={!canSave} onClick={() => submit()}>Save for next day</button>
      </div> : <button type="button" className="chief-setup-primary" disabled={!canSave} onClick={() => submit()}>{chief.phase === 'configuring' ? 'Saving…' : chief.agent ? 'Save' : `Wake ${chief.name}`}<kbd>⏎</kbd></button>}
    </>}
    {(chief.setupError || chief.restartError) && <p className="chief-setup-error" role="alert">{chief.setupError || chief.restartError}</p>}
  </div>;
}
