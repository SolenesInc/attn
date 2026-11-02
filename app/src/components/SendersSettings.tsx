import { useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useSendersStore } from '../store/senders';
import type { SenderElement } from '../types/generated';

function PairedSender({ sender, onRemove }: { sender: SenderElement; onRemove: () => void }) {
  return (
    <li className="settings-row-card" aria-label={sender.name}>
      <div>
        <p className="settings-row-title">{sender.name}</p>
        <p className="settings-row-copy">
          Paired <time dateTime={sender.created_at}>{new Date(sender.created_at).toLocaleString()}</time>
          {' · '}
          {sender.last_used_at
            ? <>Last used <time dateTime={sender.last_used_at}>{new Date(sender.last_used_at).toLocaleString()}</time></>
            : 'Never used'}
        </p>
      </div>
      <button type="button" className="settings-action" onClick={onRemove}>Remove</button>
    </li>
  );
}

export function SendersSettings() {
  const { sendSenderUnpair } = useDaemonApi();
  const senders = useSendersStore((state) => state.senders);
  const [error, setError] = useState('');

  function remove(sender: SenderElement) {
    setError('');
    sendSenderUnpair(sender.id).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
  }

  if (!senders) return <p className="settings-description">Loading paired senders…</p>;
  return (
    <section className="settings-block" aria-label="Paired senders">
      <div className="settings-block-intro">
        <h3>Paired senders</h3>
        <p className="settings-description">A removed sender is refused at once and must pair again to send.</p>
      </div>
      <div className="settings-block-body">
        {senders.senders.length > 0
          ? (
            <ul className="settings-phone-list">
              {senders.senders.map((sender) => <PairedSender key={sender.id} sender={sender} onRemove={() => remove(sender)} />)}
            </ul>
          )
          : <p className="settings-description">No paired senders. A program pairs with <code>attn send pair --name &lt;name&gt;</code>.</p>}
        {error && <p className="settings-warning" role="alert">{error}</p>}
      </div>
    </section>
  );
}
