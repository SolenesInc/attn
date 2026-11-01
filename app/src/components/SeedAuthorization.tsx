import { useEffect, useId, useRef, useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Seed, SeedAuthorization as Authorization } from '../types/generated';
import { GardenCompose } from './GardenCompose';
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
  const inputRef = useRef<HTMLInputElement | null>(null);
  const editButtonRef = useRef<HTMLButtonElement | null>(null);
  const wasEditing = useRef(false);
  const focusEditOnChange = useRef(false);
  const authorization = seed.authorization;

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
        input.value = authorization?.text ?? '';
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
      setFailure(authorization
        ? 'Write the new words, or press Clear to remove the authorization.'
        : 'Write the words that authorize this task.');
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
              {authorization ? 'Change' : 'Set'}
            </button>
            {authorization && (
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
      {authorization ? (
        <>
          <p className="seed-authorization__text">{authorization.text}</p>
          <p className="seed-authorization__meta">
            <span>set by {setterName(authorization, sessionLabels)}</span>
            <time dateTime={authorization.set_at}>{formatTime(authorization.set_at)}</time>
            {delivery && <span className={authorization.update?.typed_at ? '' : 'is-waiting'}>{delivery}</span>}
          </p>
        </>
      ) : (
        !editing && <p className="seed-authorization__empty">No authorization from the user for this task.</p>
      )}
      {editing && (
        <GardenCompose
          verb="Authorize"
          target={seed.id}
          prompt="what the user allows for this task"
          required
          busy={busy}
          inputRef={inputRef}
          onCommit={commit}
          onCancel={() => {
            setEditing(false);
            setFailure('');
          }}
        />
      )}
      {failure && <p className="seed-authorization__error" role="alert">{failure}</p>}
    </section>
  );
}
