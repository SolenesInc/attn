import { useEffect, useId, useRef, useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Seed, SeedAuthorization as Authorization } from '../types/generated';
import { whoName, type SessionLabels } from './gardenBoardModel';
import './SeedAuthorization.css';

function formatTime(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

function setterName(authorization: Authorization, sessionLabels?: SessionLabels): string {
  return authorization.set_by.ref === 'user' ? 'you' : whoName(authorization.set_by, sessionLabels);
}

function deliveryWords(authorization: Authorization, sessionLabels?: SessionLabels): string {
  const update = authorization.update;
  if (!update) return '';
  const recipient = whoName(update.recipient, sessionLabels);
  if (update.typed_at) return `typed into ${recipient} at ${formatTime(update.typed_at)}`;
  if (update.needs_approval) return `waiting for ${recipient}, which needs approval`;
  return `waiting for ${recipient} to take input`;
}

export function SeedAuthorization({
  seed,
  sessionLabels,
  className = '',
}: {
  seed: Seed;
  sessionLabels?: SessionLabels;
  className?: string;
}) {
  const daemon = useOptionalDaemonApi();
  const headingId = useId();
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState('');
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const editButtonRef = useRef<HTMLButtonElement | null>(null);
  const wasEditing = useRef(false);
  const focusEditOnChange = useRef(false);
  const authorization = seed.authorization;
  const words = authorization?.text ?? '';

  useEffect(() => {
    if (!focusEditOnChange.current) return;
    focusEditOnChange.current = false;
    editButtonRef.current?.focus();
  }, [authorization]);

  useEscapeStack(() => {
    setEditing(false);
    setFailure('');
  }, editing);

  useEffect(() => {
    if (editing) {
      wasEditing.current = true;
      const input = inputRef.current;
      if (input) {
        input.value = words;
        input.focus();
        input.select();
      }
      return;
    }
    if (wasEditing.current) {
      wasEditing.current = false;
      editButtonRef.current?.focus();
    }
  }, [editing]);

  const send = async (text: string | null) => {
    if (!daemon || busy) return;
    setBusy(true);
    setFailure('');
    try {
      await daemon.sendSeedAuthorization(seed.id, text);
      setEditing(false);
    } catch (error) {
      focusEditOnChange.current = false;
      setFailure(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  const commit = () => {
    const text = (inputRef.current?.value ?? '').trim();
    if (!text) {
      setFailure(words
        ? 'Write the new instructions, or press Clear to remove the authorization.'
        : 'Write the steps agents take on this task beyond the project\'s guidance, and where they stop.');
      inputRef.current?.focus();
      return;
    }
    void send(text);
  };

  const delivery = authorization ? deliveryWords(authorization, sessionLabels) : '';

  return (
    <section className={`seed-authorization${className ? ` ${className}` : ''}`} aria-labelledby={headingId}>
      <div className="seed-authorization__head">
        <h3 id={headingId}>Authorization</h3>
        {daemon && !editing && (
          <span className="seed-authorization__actions">
            <button ref={editButtonRef} type="button" onClick={() => setEditing(true)} disabled={busy}>
              {words ? 'Change' : 'Set'}
            </button>
            {words && (
              <button
                type="button"
                onClick={() => {
                  focusEditOnChange.current = true;
                  void send(null);
                }}
                disabled={busy}
              >
                Clear
              </button>
            )}
          </span>
        )}
      </div>
      {words ? (
        <p className="seed-authorization__text">{words}</p>
      ) : (
        !editing && <p className="seed-authorization__empty">No authorization from the user for this task.</p>
      )}
      {authorization && (
        <p className="seed-authorization__meta">
          <span>{words ? 'set' : 'cleared'} by {setterName(authorization, sessionLabels)}</span>
          <time dateTime={authorization.set_at}>{formatTime(authorization.set_at)}</time>
          {delivery && <span className={authorization.update?.typed_at ? '' : 'is-waiting'}>{delivery}</span>}
        </p>
      )}
      {editing && (
        <div className="seed-authorization__editor">
          <textarea
            ref={inputRef}
            className="seed-authorization__input"
            rows={3}
            spellCheck={false}
            disabled={busy}
            placeholder="Land on main once the checks pass. Ask me before releasing."
            aria-label={`Authorize ${seed.id}: the steps agents take beyond the project's guidance, and where they stop`}
            onKeyDown={(event) => {
              event.stopPropagation();
              if (event.nativeEvent.isComposing || event.keyCode === 229) return;
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                commit();
              }
              if (event.key === 'Escape') {
                event.preventDefault();
                setEditing(false);
                setFailure('');
              }
            }}
          />
          <span className="seed-authorization__keys">
            <kbd>⏎</kbd> save <kbd>⇧⏎</kbd> new line <kbd>esc</kbd> cancel
          </span>
        </div>
      )}
      {failure && <p className="seed-authorization__error" role="alert">{failure}</p>}
    </section>
  );
}
