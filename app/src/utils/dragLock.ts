// WebKit does not honor preventDefault on pointerdown for selection purposes, so a drag
// still paints a text selection; disabling user-select on <body> is the reliable fix.
type BodyStyle = CSSStyleDeclaration & { webkitUserSelect?: string };

export function lockTextSelection(cursor?: string): () => void {
  const body = document.body;
  const style = body.style as BodyStyle;
  const prevUserSelect = style.userSelect;
  const prevWebkitUserSelect = style.webkitUserSelect ?? '';
  const prevCursor = style.cursor;

  style.userSelect = 'none';
  style.webkitUserSelect = 'none';
  if (cursor) {
    style.cursor = cursor;
  }

  const swallow = (event: Event) => event.preventDefault();
  document.addEventListener('selectstart', swallow);

  return () => {
    document.removeEventListener('selectstart', swallow);
    style.userSelect = prevUserSelect;
    style.webkitUserSelect = prevWebkitUserSelect;
    style.cursor = prevCursor;
  };
}
