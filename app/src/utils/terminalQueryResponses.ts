const CURSOR_POSITION_REPORT_RE = /\x1b\[\d+;\d+R/g;
const DEVICE_ATTRIBUTES_RESPONSE_RE = /\x1b\[\?[0-9;]*c/g;
const OSC_COLOR_RESPONSE_RE = /\x1b\]1[012];[^\x07\x1b]*(?:\x07|\x1b\\)/g;

// The daemon-side worker is the single authority for CPR, DA1 and OSC 10/11/12 replies;
// the shell reads a duplicate from the local model as stray input.
export function stripDaemonOwnedResponses(response: string): string {
  return response
    .replace(CURSOR_POSITION_REPORT_RE, '')
    .replace(DEVICE_ATTRIBUTES_RESPONSE_RE, '')
    .replace(OSC_COLOR_RESPONSE_RE, '');
}
