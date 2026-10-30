import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { outpostNeedsUpdate, outpostIsUpdating, outpostUpdateText } from './OutpostUpdateBanner';
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

export function OutpostsSettings({ onOpenTerminal }: { onOpenTerminal: () => void }) {
  const { outposts, outpostCommandError, sendAddOutpost, sendRepairOutpost, sendConnectOutpostInTerminal, sendRemoveOutpost, sendUpdateOutpost, homeBuild, isConnected } = useDaemonApi();
  const sessions = useDaemonStore((state) => state.daemonSessions);
  const removeButton = useRef<HTMLButtonElement>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [destination, setDestination] = useState('');
  const [submitted, setSubmitted] = useState<{ name: string; destination: string } | null>(null);

  useEffect(() => {
    if (!submitted || !outposts.some(row => row.name === submitted.name && row.destination === submitted.destination)) return;
    if ((name.trim() || destination.trim()) === submitted.name && destination.trim() === submitted.destination) {
      setName('');
      setDestination('');
    }
    setSubmitted(null);
  }, [outposts, submitted, name, destination]);

  function add(event: FormEvent) {
    event.preventDefault();
    const row = { name: name.trim() || destination.trim(), destination: destination.trim() };
    if (sendAddOutpost(row.name, row.destination) && !outposts.some(existing => existing.destination === row.destination)) setSubmitted(row);
  }

  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Add an outpost</h3>
          <p className="settings-description">Add the destination you use with ssh. attn installs your home's build there and pins its identity on first contact.</p>
        </div>
        <form className="settings-block-body" aria-label="Add an outpost" onSubmit={add}>
          <div className="settings-field">
            <label className="settings-label" htmlFor="outpost-name">Name</label>
            <input id="outpost-name" className="settings-input" value={name} onChange={event => setName(event.target.value)} placeholder={destination.trim() || 'Optional'} />
          </div>
          <div className="settings-field">
            <label className="settings-label" htmlFor="outpost-destination">ssh destination</label>
            <input id="outpost-destination" className="settings-input" value={destination} onChange={event => setDestination(event.target.value)} placeholder="user@host" autoCapitalize="none" autoCorrect="off" spellCheck={false} required />
          </div>
          <div><button type="submit" className="settings-action" disabled={!isConnected || !destination.trim()}>Add outpost</button></div>
        </form>
      </section>
      {outpostCommandError && <div className="settings-warning" role="alert">{outpostCommandError}</div>}
      {!isConnected && <p className="settings-warning" role="status">Connect to the home daemon to change outposts.</p>}
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Outposts</h3>
          <p className="settings-description">Re-pair forgets the pinned host key and daemon identity, then connects again. Remove closes its terminals across all profiles, releases enrollment and stops its daemon over ssh, then forgets this outpost. Its binary, data and Notebook stay. If ssh cannot reach it, run this instance's attn enrollment leave and attn daemon stop commands there.</p>
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
                      <div><h4 className="settings-row-title">{row.name}</h4><p className="settings-row-copy">{row.destination}</p></div>
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
                      {row.state === 'refused' && row.connectable && <button type="button" className="settings-action" disabled={!isConnected} onClick={() => { if (sendConnectOutpostInTerminal(row.id)) onOpenTerminal(); }}>Connect in a terminal</button>}
                      {outpostNeedsUpdate(row, homeBuild) && <button type="button" className="settings-action" disabled={!isConnected || outpostIsUpdating(row)} onClick={() => sendUpdateOutpost(row.id)}>{outpostIsUpdating(row) ? 'Updating…' : 'Update to home build'}</button>}
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
                      <p className="settings-description" role="status">{row.name} isn't connected. If ssh cannot stop it, its daemon and shells keep running there. Run this instance's attn enrollment leave and attn daemon stop commands there.</p>
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
