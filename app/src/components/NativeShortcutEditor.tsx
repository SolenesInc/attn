import { useEffect, useRef, useState } from 'react';
import { isMacLikePlatform } from '../shortcuts/platform';

export interface NativeShortcut {
  label: string;
  title: string;
  description: string;
  binding: string | null;
  active: string | null;
  error?: string;
  defaultBinding: string;
  setBinding: (binding: string | null) => Promise<void>;
}

interface NativeShortcutEditorProps {
  shortcut: NativeShortcut;
  recording: boolean;
  onStart: () => void;
  onCancel: () => void;
}

export function NativeShortcutEditor({ shortcut, recording, onStart, onCancel }: NativeShortcutEditorProps) {
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const recorder = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!recording) return;
    recorder.current?.focus();
  }, [recording]);
  if (!isMacLikePlatform()) return null;
  async function bind(binding: string | null) {
    onCancel(); setSaving(true);
    try { await shortcut.setBinding(binding); setError(''); }
    catch (error) { setError(String(error)); }
    finally { setSaving(false); }
  }
  return <section className="shortcut-editor-category" aria-label={shortcut.label}>
    <h3 className="shortcut-editor-category-title">{shortcut.title}</h3>
    <p>{shortcut.description}</p>
    <div className="shortcut-editor-row">
      <span>{shortcut.binding || 'Off'}</span>
      <button ref={recorder} disabled={saving} className="shortcut-editor-btn" onClick={recording ? onCancel : onStart} onBlur={onCancel} onKeyDown={event => {
        if (!recording) return;
        event.preventDefault(); event.stopPropagation();
        if (event.key === 'Escape') { onCancel(); return; }
        if (['Meta', 'Control', 'Alt', 'Shift'].includes(event.key)) return;
        if (!event.metaKey && !event.ctrlKey && !event.altKey) { setError('Use Command, Control or Option with another key.'); return; }
        const binding = [event.metaKey && 'Super', event.ctrlKey && 'Control', event.altKey && 'Alt', event.shiftKey && 'Shift', event.code].filter(Boolean).join('+');
        void bind(binding);
      }}>{recording ? 'Press shortcut…' : 'Change'}</button>
      <button disabled={saving} className="shortcut-editor-btn" onClick={() => void bind(shortcut.defaultBinding)}>Reset</button>
      <button disabled={saving} className="shortcut-editor-btn" onClick={() => void bind(null)}>Turn off</button>
    </div>
    {(error || shortcut.error) && <p role="alert">{error || shortcut.error}</p>}
    <p>Active: {shortcut.active || 'Off'}</p>
    <small>macOS system and exclusive conflicts are reported. Shared shortcuts may not be detectable.</small>
  </section>;
}
