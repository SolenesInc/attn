import { isTauri } from '@tauri-apps/api/core';

// Tauri's native clipboard bypasses the webview permission model. Use it for writes
// outside a user gesture (OSC 52 on the PTY stream, rapid onSelectionChange).
export async function writeClipboardText(text: string): Promise<void> {
  if (isTauri()) {
    const { writeText } = await import('@tauri-apps/plugin-clipboard-manager');
    await writeText(text);
    return;
  }
  await navigator.clipboard.writeText(text);
}

export async function readClipboardText(): Promise<string> {
  if (isTauri()) {
    const { readText } = await import('@tauri-apps/plugin-clipboard-manager');
    return (await readText()) ?? '';
  }
  return navigator.clipboard.readText();
}
