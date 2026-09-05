import { useEffect } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { nextAnimationFrame, settleBeforeBridgeRequest } from './uiAutomationSettle';

// Wire strings shared with useUiAutomationBridge.ts by value: ui_automation.rs
// broadcasts these to EVERY webview window, so both bridges must agree.
const UI_AUTOMATION_REQUEST_EVENT = 'attn://ui-automation/request';
const UI_AUTOMATION_RESPONSE_EVENT = 'attn://ui-automation/response';
const UI_AUTOMATION_READY_EVENT = 'attn://ui-automation/ready';

const PRESENT_WINDOW_ACTION_PREFIX = 'present_window_';

// The request carries no window field, so the action-name prefix is the only routing:
// exactly one bridge listener may answer, since Rust resolves on the first response.
export function isPresentWindowAction(action: string): boolean {
  return action.startsWith(PRESENT_WINDOW_ACTION_PREFIX);
}

interface AutomationRequest {
  request_id: string;
  action: string;
  payload?: Record<string, unknown> | null;
}

interface AutomationResponse {
  request_id: string;
  ok: boolean;
  result?: unknown;
  error?: string;
}

async function waitForSubmitDialog(timeoutMs = 1_000): Promise<HTMLElement> {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    const dialog = document.querySelector<HTMLElement>('.present-root-submit-dialog');
    if (dialog) return dialog;
    await nextAnimationFrame();
  }
  throw new Error('present_window_submit: submit dialog did not appear');
}

// Submit-dialog buttons are addressed by class, never by position: their order is
// not a contract.
const SUBMIT_DIALOG_ACTION_CLASS: Record<string, string> = {
  feedback: 'present-root-submit-feedback',
  approve: 'present-root-submit-approve',
  close: 'present-root-submit-close',
};

async function handlePresentWindowAction(action: string, payload?: Record<string, unknown> | null): Promise<unknown> {
  switch (action) {
    case 'present_window_is_visible': {
      return { visible: await getCurrentWindow().isVisible() };
    }
    case 'present_window_submit': {
      const dialogAction = typeof payload?.action === 'string' ? payload.action : 'feedback';
      const buttonClass = SUBMIT_DIALOG_ACTION_CLASS[dialogAction];
      if (!buttonClass) {
        throw new Error(`present_window_submit: unknown action "${dialogAction}"`);
      }

      const submitButton = document.querySelector<HTMLElement>('.present-drive-bar-submit');
      if (!submitButton) {
        throw new Error('present_window_submit: submit button not found');
      }
      submitButton.click();

      const dialog = await waitForSubmitDialog();
      const confirmButton = dialog.querySelector<HTMLElement>(`.${buttonClass}`);
      if (!confirmButton) {
        throw new Error(`present_window_submit: "${dialogAction}" button not found in submit dialog`);
      }
      confirmButton.click();
      return { submitted: true, action: dialogAction };
    }
    default:
      throw new Error(`Unknown present-window automation action: ${action}`);
  }
}

export function usePresentAutomationBridge(): void {
  useEffect(() => {
    const automationEnabled =
      typeof window !== 'undefined' &&
      (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
    if (!isTauri() || !automationEnabled) {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
      if (!isPresentWindowAction(request.action)) {
        return;
      }

      let response: AutomationResponse;
      try {
        await settleBeforeBridgeRequest(request.action);
        const result = await handlePresentWindowAction(request.action, request.payload);
        response = { request_id: request.request_id, ok: true, result };
      } catch (error) {
        response = {
          request_id: request.request_id,
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        };
      }
      await emit(UI_AUTOMATION_RESPONSE_EVENT, response);
    });

    return () => {
      void unlistenPromise.then((unlisten) => unlisten());
    };
  }, []);
}
