import { useRef, useState } from 'react';
import FocusTrap from './AppFocusTrap';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useSendersStore } from '../store/senders';
import type { SenderPairing } from '../types/generated';
import './SenderPairingPrompt.css';

const ignoreEscape = () => {};

function oldestRequest(requests: SenderPairing[]): SenderPairing | undefined {
  return [...requests].sort((a, b) => a.requested_at.localeCompare(b.requested_at))[0];
}

function PairingRequest({ request }: { request: SenderPairing }) {
  const { sendSenderPairDecide } = useDaemonApi();
  const denyRef = useRef<HTMLButtonElement>(null);
  const [deciding, setDeciding] = useState(false);
  const [error, setError] = useState('');

  useEscapeStack(ignoreEscape, true);

  function decide(allow: boolean) {
    setDeciding(true);
    setError('');
    sendSenderPairDecide(request.id, allow)
      .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)))
      .finally(() => setDeciding(false));
  }

  return (
    <div className="sender-pairing-overlay" role="presentation">
      <FocusTrap focusTrapOptions={{ initialFocus: () => denyRef.current ?? false, escapeDeactivates: false }}>
        <section
          className="sender-pairing-content"
          role="dialog"
          aria-modal="true"
          aria-labelledby="sender-pairing-title"
          aria-describedby="sender-pairing-message"
        >
          <h2 className="sender-pairing-title" id="sender-pairing-title">Allow “{request.name}” to send messages as you?</h2>
          <p className="sender-pairing-message" id="sender-pairing-message">
            This program will be able to send files and messages that agents receive as your own.
          </p>
          <p className="sender-pairing-code">{request.code}</p>
          <p className="sender-pairing-hint">Check that the program shows the same code.</p>
          {error && <p className="sender-pairing-error" role="alert">{error}</p>}
          <div className="sender-pairing-actions">
            <button ref={denyRef} type="button" className="deny" disabled={deciding} onClick={() => decide(false)}>Deny</button>
            <button type="button" className="allow" disabled={deciding} onClick={() => decide(true)}>Allow</button>
          </div>
        </section>
      </FocusTrap>
    </div>
  );
}

export function SenderPairingPrompt() {
  const requests = useSendersStore((state) => state.senders?.requests);
  const request = requests ? oldestRequest(requests) : undefined;
  if (!request) return null;
  return <PairingRequest key={request.id} request={request} />;
}
