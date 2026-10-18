import { useEffect, useState, type FormEvent } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import type { OutpostInfo } from '../types/generated';

const stateTone: Record<OutpostInfo['state'], string> = {
  connecting: '', connected: 'good', stalled: 'warn', lost: 'bad', refused: 'bad',
};

function Timestamp({ value }: { value: string }) {
  return <time dateTime={value} title={value}>{new Date(value).toLocaleString()}</time>;
}

export function OutpostsSettings() {
  const { outposts, outpostCommandError, sendAddOutpost, sendRepairOutpost, sendRemoveOutpost, isConnected } = useDaemonApi();
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
          <p className="settings-description">Connect to an outpost running the same attn build. Its host key is pinned on first contact.</p>
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
          <h3>Bridges</h3>
          <p className="settings-description">Re-pair forgets the pinned host key and daemon identity, then connects again. Remove disconnects the bridge and forgets this outpost.</p>
        </div>
        <div className="settings-block-body">
          {outposts.length === 0 ? <p className="settings-empty">No outposts added.</p> : (
            <ul className="settings-outposts" aria-label="Outposts">
              {outposts.map(row => (
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
                  <div className="settings-outpost-actions">
                    <button type="button" className="settings-action" disabled={!isConnected} onClick={() => sendRepairOutpost(row.id)}>Re-pair</button>
                    <button type="button" className="settings-action" disabled={!isConnected} onClick={() => sendRemoveOutpost(row.id)}>Remove</button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </>
  );
}
