import type { CrewCharterAutosave, CrewCharterEdit } from '../hooks/useCrewCharterAutosave';
import type { CrewMember } from '../types/generated';

function charterStatus(edit: CrewCharterEdit): string {
  if (edit.state === 'dirty') return 'Waiting to save';
  if (edit.state === 'saving') return 'Saving…';
  if (edit.state === 'conflict') return 'Changed elsewhere';
  if (edit.state === 'error') return 'Not saved';
  return 'Saved';
}

export function CrewCharterTab({ member, edit, autosave }: {
  member: CrewMember;
  edit?: CrewCharterEdit;
  autosave: CrewCharterAutosave;
}) {
  if (!edit || edit.state === 'loading') {
    return <div className="crew-document-state">Loading charter…</div>;
  }
  if (!edit.acknowledged) {
    return (
      <div className="crew-document-state is-error" role="alert">
        <span>{edit.error || 'The charter could not be loaded.'}</span>
        <button type="button" data-testid="crew-charter-load-retry" onClick={() => void autosave.load(member.key)}>Retry</button>
      </div>
    );
  }
  return (
    <section className="crew-charter" aria-label="Charter editor">
      <div className="crew-document-heading">
        <h3>Charter</h3>
        <span data-testid="crew-charter-status" className={`crew-document-save is-${edit.state}`} role="status" aria-live="polite">{charterStatus(edit)}</span>
      </div>
      <div className="crew-charter-meta"><span>CHARTER.md</span><span>Markdown</span></div>
      <label className="crew-visually-hidden" htmlFor={`crew-charter-${member.key}`}>Charter for {member.name}</label>
      <textarea
        id={`crew-charter-${member.key}`}
        data-testid="crew-charter-editor"
        className="crew-charter-editor"
        value={edit.draft}
        onChange={(event) => autosave.update(member.key, event.target.value)}
        onBlur={() => void autosave.flush(member.key)}
        spellCheck
      />
      {edit.state === 'error' && (
        <div className="crew-document-error" role="alert">
          <span>{edit.error || 'The charter was not saved. Your edit is still here.'}</span>
          <button type="button" data-testid="crew-charter-save-retry" onClick={() => void autosave.retry(member.key)}>Retry</button>
        </div>
      )}
      {edit.state === 'conflict' && edit.conflict && (
        <div className="crew-charter-conflict" role="alert">
          <div>
            <strong>The file changed outside this editor.</strong>
            <span>Your edit is still here. Choose which version should become canonical.</span>
          </div>
          <button type="button" data-testid="crew-charter-use-file" onClick={() => autosave.useExternal(member.key)}>Use file version</button>
          <button type="button" data-testid="crew-charter-keep-mine" onClick={() => void autosave.keepMine(member.key)}>Keep my edit</button>
        </div>
      )}
    </section>
  );
}
