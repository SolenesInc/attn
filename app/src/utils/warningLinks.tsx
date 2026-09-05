import { Fragment, type ReactNode } from 'react';

const URL_PATTERN = /https?:\/\/[^\s]+/g;
const TRAILING_PUNCT = /[.,;:!?)\]]+$/;

export interface WarningLinkSegment {
  key: string;
  text?: string;
  url?: string;
  label?: string;
}

// compactUrlLabel shrinks a URL to its host for banner display: the full URL
// stays on the button's title and is what opens.
export function compactUrlLabel(url: string): string {
  try {
    const parsed = new URL(url);
    return parsed.pathname && parsed.pathname !== '/' ? `${parsed.host}/…` : parsed.host;
  } catch {
    return url.length > 24 ? `${url.slice(0, 23)}…` : url;
  }
}

// splitWarningLinks cuts bare URLs out of a warning message so the banner can
// render them as compact actions instead of full-width raw text.
export function splitWarningLinks(message: string): WarningLinkSegment[] {
  const segments: WarningLinkSegment[] = [];
  let last = 0;
  let n = 0;
  for (const match of message.matchAll(URL_PATTERN)) {
    let url = match[0];
    const punct = url.match(TRAILING_PUNCT);
    const suffix = punct ? punct[0] : '';
    if (suffix) url = url.slice(0, -suffix.length);
    if (match.index > last) segments.push({ key: `t${n++}`, text: message.slice(last, match.index) });
    segments.push({ key: `u${n++}`, url, label: compactUrlLabel(url) });
    last = match.index + match[0].length;
    if (suffix) segments.push({ key: `t${n++}`, text: suffix });
  }
  if (last < message.length) segments.push({ key: `t${n++}`, text: message.slice(last) });
  return segments;
}

// renderWarningSegments renders a warning message with compact link actions.
// onOpenUrl opens the full URL; the banner stays a single short line.
export function renderWarningSegments(message: string, onOpenUrl: (url: string) => void): ReactNode[] {
  return splitWarningLinks(message).map((segment) =>
    segment.url ? (
      <button
        key={segment.key}
        type="button"
        className="warning-link"
        title={segment.url}
        onClick={() => onOpenUrl(segment.url as string)}
      >
        {segment.label}
      </button>
    ) : (
      <Fragment key={segment.key}>{segment.text}</Fragment>
    ),
  );
}
