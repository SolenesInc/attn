import { useMemo, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { DiagnosticPaneDescriptor, PendingDiagnosticCapture } from '../utils/diagnosticReport';
import './DiagnosticReportPrompt.css';

interface DiagnosticReportPromptProps {
  capture: PendingDiagnosticCapture;
  affectedPaneId: string | null;
  onCreate: (selectedPaneIds: string[]) => Promise<void>;
  onClose: () => void;
}

export function DiagnosticReportPrompt({
  capture,
  affectedPaneId,
  onCreate,
  onClose,
}: DiagnosticReportPromptProps) {
  const [selected, setSelected] = useState(() => new Set(
    affectedPaneId && capture.panes.some((pane) => pane.paneId === affectedPaneId && pane.available)
      ? [affectedPaneId]
      : [],
  ));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const groups = useMemo(() => {
    const grouped = new Map<string, { label: string; panes: DiagnosticPaneDescriptor[] }>();
    for (const pane of capture.panes) {
      const existing = grouped.get(pane.workspaceId);
      if (existing) existing.panes.push(pane);
      else grouped.set(pane.workspaceId, { label: pane.workspaceLabel, panes: [pane] });
    }
    return [...grouped.entries()];
  }, [capture.panes]);

  useEscapeStack(onClose, !saving);

  const create = async () => {
    setSaving(true);
    setError(null);
    try {
      await onCreate([...selected]);
      onClose();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'The report could not be saved');
      setSaving(false);
    }
  };

  return (
    <div className="diagnostic-report-overlay" onClick={saving ? undefined : onClose}>
      <FocusTrap focusTrapOptions={{ allowOutsideClick: true, escapeDeactivates: false }}>
        <section
          className="diagnostic-report-sheet"
          role="dialog"
          aria-modal="true"
          aria-labelledby="diagnostic-report-title"
          onClick={(event) => event.stopPropagation()}
        >
          <header className="diagnostic-report-header">
            <div>
              <h2 id="diagnostic-report-title">Create diagnostic report</h2>
              <p>Save one file with the evidence needed to trace a stuck terminal.</p>
            </div>
            <button type="button" aria-label="Close diagnostic report" onClick={onClose} disabled={saving}>×</button>
          </header>

          <div className="diagnostic-report-included">
            <strong>Included automatically</strong>
            <p>App and daemon versions, session and pane metadata, shortened working directories, geometry, health events, and a recent content-free input trace.</p>
            <p>Environment variables, credentials, clipboard data, raw prompt bodies, raw terminal bytes, logs, and error text stay out.</p>
          </div>

          <div className="diagnostic-report-output">
            <div className="diagnostic-report-output-heading">
              <div>
                <strong>Recent terminal output</strong>
                <span>Optional. Output may contain private text or secrets.</span>
              </div>
              {selected.size > 0 && (
                <button type="button" onClick={() => setSelected(new Set())} disabled={saving}>Clear</button>
              )}
            </div>
            {groups.length === 0 ? (
              <p className="diagnostic-report-empty">No mounted terminal panes are available.</p>
            ) : groups.map(([workspaceId, group]) => (
              <fieldset key={workspaceId}>
                <legend>{group.label}</legend>
                {group.panes.map((pane) => (
                  <label key={pane.paneId} className={!pane.available ? 'is-unavailable' : undefined}>
                    <input
                      type="checkbox"
                      checked={selected.has(pane.paneId)}
                      disabled={saving || !pane.available}
                      onChange={(event) => {
                        const next = new Set(selected);
                        if (event.target.checked) next.add(pane.paneId);
                        else next.delete(pane.paneId);
                        setSelected(next);
                      }}
                    />
                    <span>
                      <b>{pane.title}</b>
                      <small>{pane.available ? pane.sessionLabel : `${pane.sessionLabel}, not currently mounted`}</small>
                    </span>
                  </label>
                ))}
              </fieldset>
            ))}
          </div>

          {error && <div className="diagnostic-report-error" role="alert">{error}</div>}
          <footer className="diagnostic-report-actions">
            <button type="button" className="secondary" onClick={onClose} disabled={saving}>Cancel</button>
            <button type="button" className="primary" onClick={() => { void create(); }} disabled={saving} autoFocus>
              {saving ? 'Saving…' : 'Save report'}
            </button>
          </footer>
        </section>
      </FocusTrap>
    </div>
  );
}
