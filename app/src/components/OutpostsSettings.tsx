import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { outpostIsUpdating, outpostUpdateText } from './OutpostUpdateBanner';
import type { OutpostInfo, OutpostNotebook } from '../types/generated';

const stateTone: Record<OutpostInfo['state'], string> = {
  connecting: '', connected: 'good', stalled: 'warn', lost: 'bad', refused: 'bad',
};

function Timestamp({ value }: { value: string }) {
  return <time dateTime={value} title={value}>{new Date(value).toLocaleString()}</time>;
}

function NotebookSyncStatus({ notebook }: { notebook: OutpostNotebook }) {
  const bytes = notebook.need_bytes ?? 0;
  const items = notebook.need_items ?? 0;
  const unit = bytes >= 1024 ** 3 ? 'GB' : bytes >= 1024 ** 2 ? 'MB' : bytes >= 1024 ? 'kB' : 'B';
  const divisor = { GB: 1024 ** 3, MB: 1024 ** 2, kB: 1024, B: 1 }[unit];
  const remaining = `${divisor === 1 ? bytes : (bytes / divisor).toFixed(1)} ${unit} and ${items} ${items === 1 ? 'item' : 'items'} left`;
  const label = { first_sync: 'first sync', syncing: 'syncing', in_sync: 'in sync', error: 'error' }[notebook.state];
  const conflicts = notebook.conflicts ?? 0;
  return <div className="settings-outpost-notebook" role="status">
    <p className={notebook.state === 'error' ? 'settings-warning' : 'settings-description'}>
      {notebook.state === 'error' ? `Notebook sync error: ${notebook.error}` : `Notebook: ${label}${(bytes > 0 || items > 0) ? ` · ${remaining}` : ''}`}
    </p>
    {conflicts > 0 && <p className="settings-warning">{conflicts} conflict {conflicts === 1 ? 'copy' : 'copies'}</p>}
    {notebook.previous_root && <p className="settings-description">Previous folder saved at {notebook.previous_root}</p>}
  </div>;
}

export function OutpostsSettings() {
  const { outposts, outpostCommandError, sendAddOutpost, sendRepairOutpost, sendRemoveOutpost, sendUpdateOutpost, homeBuild, isConnected } = useDaemonApi();
  const sessions = useDaemonStore((state) => state.daemonSessions);
  const removeButton = useRef<HTMLButtonElement>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [address, setAddress] = useState('');
  const [submitted, setSubmitted] = useState<{ name: string; address: string } | null>(null);

  useEffect(() => {
    if (!submitted || !outposts.some(row => row.name === submitted.name && row.address === submitted.address)) return;
    if (name.trim() === submitted.name && address.trim() === submitted.address) {
      setName('');
      setAddress('');
    }
    setSubmitted(null);
  }, [outposts, submitted, name, address]);

  function add(event: FormEvent) {
    event.preventDefault();
    const row = { name: name.trim(), address: address.trim() };
    if (sendAddOutpost(row.name, row.address) && !outposts.some(existing => existing.address === row.address)) setSubmitted(row);
  }

  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Add an outpost</h3>
          <p className="settings-description">Connect to attn on another computer running the same build. Its host key is pinned on first contact.</p>
        </div>
        <form className="settings-block-body" aria-label="Add an outpost" onSubmit={add}>
          <div className="settings-field">
            <label className="settings-label" htmlFor="outpost-name">Name</label>
            <input id="outpost-name" className="settings-input" value={name} onChange={event => setName(event.target.value)} required />
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="outpost-address">Address</label>
            <input id="outpost-address" className="settings-input" value={address} onChange={event => setAddress(event.target.value)} placeholder="hostname:port" autoCapitalize="none" autoCorrect="off" spellCheck={false} required />
          </div>
          <div><button type="submit" className="settings-action" disabled={!isConnected || !name.trim() || !address.trim()}>Add outpost</button></div>
        </form>
      </section>
      {outpostCommandError && <div className="settings-warning" role="alert">{outpostCommandError}</div>}
      {!isConnected && <p className="settings-warning" role="status">Connect to the home daemon to change outposts.</p>}
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Outposts</h3>
          <p className="settings-description">Re-pair forgets the pinned host key and daemon identity, then connects again. Remove closes its terminals across all profiles, then disconnects and forgets this outpost. While it isn't connected, its shells keep running there until you run attn enrollment leave on it.</p>
        </div>
        <div className="settings-block-body">
          {outposts.length === 0 ? <p className="settings-empty">No outposts added.</p> : (
            <ul className="settings-outposts" aria-label="Outposts">
              {outposts.map(row => {
                const count = sessions.filter((session) => session.host === row.id).length;
                const terminals = `${count} ${count === 1 ? 'terminal' : 'terminals'}`;
                const confirmRemove = row.reach === 'reachable' ? `Remove and close ${terminals}` : `Remove and forget ${terminals}`;
                return (
                  <li key={row.id} className="settings-outpost" aria-label={row.name}>
                    <div className="settings-row-card">
                      <div><h4 className="settings-row-title">{row.name}</h4><p className="settings-row-copy">{row.address}</p></div>
                      <span className={`settings-pill ${stateTone[row.state]}`}>{row.state}</span>
                    </div>
                    <div className="settings-meta-row"><span>Since</span><Timestamp value={row.state_since} /></div>
                    <div className="settings-meta-row"><span>Build</span><code>{row.build || 'Not yet known'}</code></div>
                    {row.daemon_id && <div className="settings-meta-row"><span>Daemon</span><code>{row.daemon_id}</code></div>}
                    {row.host_key_fingerprint && <div className="settings-meta-row"><span>Host key</span><code>{row.host_key_fingerprint}</code></div>}
                    {row.last_error && <div className="settings-warning settings-outpost-error"><p>{row.last_error}</p>{row.last_error_at && <Timestamp value={row.last_error_at} />}</div>}
                    {row.notebook && <NotebookSyncStatus notebook={row.notebook} />}
                    {row.update && <p className="settings-description" role="status">{outpostUpdateText(row)}</p>}
                    <div className="settings-outpost-actions">
                      {row.build && homeBuild && row.build !== homeBuild && <button type="button" className="settings-action" disabled={!isConnected || outpostIsUpdating(row)} onClick={() => sendUpdateOutpost(row.id)}>{outpostIsUpdating(row) ? 'Updating…' : 'Update to home build'}</button>}
                      <button type="button" className="settings-action" disabled={!isConnected} onClick={() => sendRepairOutpost(row.id)}>Re-pair</button>
                      <button
                        ref={removing === row.id ? removeButton : undefined}
                        type="button"
                        className="settings-action"
                        disabled={!isConnected}
                        onClick={() => {
                          if (count > 0 && removing !== row.id) {
                            setRemoving(row.id);
                            return;
                          }
                          sendRemoveOutpost(row.id);
                          setRemoving(null);
                        }}
                      >
                        {removing === row.id && count > 0 ? confirmRemove : 'Remove'}
                      </button>
                      {removing === row.id && count > 0 ? (
                        <button type="button" className="settings-action" onClick={() => { removeButton.current?.focus(); setRemoving(null); }}>Cancel</button>
                      ) : null}
                    </div>
                    {removing === row.id && count > 0 && row.reach !== 'reachable' ? (
                      <p className="settings-description" role="status">{row.name} isn't connected, so its shells keep running there until you run attn enrollment leave on it.</p>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </section>
    </>
  );
}
