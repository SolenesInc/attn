export type TextField = HTMLInputElement | HTMLTextAreaElement;

const TEXT_INPUT_TYPES = new Set(['text', 'search', 'url', 'email', 'tel', '']);

export function writableField(element: Element | null): TextField | null {
  if (element instanceof HTMLTextAreaElement) return element.readOnly || element.disabled ? null : element;
  if (element instanceof HTMLInputElement) {
    if (!TEXT_INPUT_TYPES.has(element.type) || element.readOnly || element.disabled) return null;
    return element;
  }
  return null;
}
