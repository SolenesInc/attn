import { useEffect, useRef, useState } from 'react';
import { useCaptureShortcut } from '../quickCapture/ShortcutContext';
import { DEFAULT_QUICK_CAPTURE_SHORTCUT } from '../quickCapture/client';
import { isMacLikePlatform } from '../shortcuts/platform';

interface QuickCaptureShortcutEditorProps {
  recording: boolean;
  onStart: () => void;
  onCancel: () => void;
}

export function QuickCaptureShortcutEditor({ recording, onStart, onCancel }: QuickCaptureShortcutEditorProps) {
  const controller = useCaptureShortcut();
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const recorder = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!recording) return;
    recorder.current?.focus();
  }, [recording]);
  if (!isMacLikePlatform() || !controller) return null;
  async function bind(binding: string | null) {
    onCancel(); setSaving(true);
    try { await controller!.setBinding(binding); setError(''); }
    catch (error) { setError(String(error)); }
    finally { setSaving(false); }
  }
  return <section className="shortcut-editor-category" aria-label="Global Quick Capture shortcut">
    <h3 className="shortcut-editor-category-title">Quick Capture · system-wide</h3>
    <p>Open Quick Capture over the current app. Escape keeps your draft.</p>
    <div className="shortcut-editor-row">
      <span>{controller.state.binding || 'Off'}</span>
      <button ref={recorder} disabled={saving} className="shortcut-editor-btn" onClick={recording ? onCancel : onStart} onBlur={onCancel} onKeyDown={event => {
        if (!recording) return;
        event.preventDefault(); event.stopPropagation();
        if (event.key === 'Escape') { onCancel(); return; }
        if (['Meta', 'Control', 'Alt', 'Shift'].includes(event.key)) return;
        if (!event.metaKey && !event.ctrlKey && !event.altKey) { setError('Use Command, Control or Option with another key.'); return; }
        const binding = [event.metaKey && 'Super', event.ctrlKey && 'Control', event.altKey && 'Alt', event.shiftKey && 'Shift', event.code].filter(Boolean).join('+');
        void bind(binding);
      }}>{recording ? 'Press shortcut…' : 'Change'}</button>
      <button disabled={saving} className="shortcut-editor-btn" onClick={() => void bind(DEFAULT_QUICK_CAPTURE_SHORTCUT)}>Reset</button>
      <button disabled={saving} className="shortcut-editor-btn" onClick={() => void bind(null)}>Turn off</button>
    </div>
    {(error || controller.state.shortcutError) && <p role="alert">{error || controller.state.shortcutError}</p>}
    <p>Active: {controller.state.activeBinding || 'Off'}</p>
    <small>macOS system and exclusive conflicts are reported. Shared shortcuts may not be detectable.</small>
  </section>;
}
