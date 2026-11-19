import { writableField } from './textFields';
import { isDomInstance } from './realm';

export function installNativeTextUndo(root: Document): () => void {
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.defaultPrevented || !event.metaKey || event.ctrlKey || event.altKey || event.key.toLowerCase() !== 'z') return;
    if (!writableField(isDomInstance(event.target, 'Element') ? event.target : null)) return;
    event.preventDefault();
    root.execCommand(event.shiftKey ? 'redo' : 'undo');
  };
  root.addEventListener('keydown', onKeyDown);
  return () => root.removeEventListener('keydown', onKeyDown);
}
