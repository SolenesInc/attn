import type { AutomationFormValues } from './automationFormModel';

export interface AutomationFormAutomationState {
  present: boolean;
  mode: 'create' | 'edit';
  definitionId: string | null;
  revision: number;
  status: 'loading' | 'ready' | 'load-error';
  loadError: string;
  values: AutomationFormValues;
  errors: Record<string, string>;
  saving: boolean;
  saveError: string;
  saveErrorCode: string;
  enabled: boolean | null;
  compiledSentence: string;
  deleteArmed: boolean;
}

export interface AutomationFormAutomationHandle {
  getState(): AutomationFormAutomationState;
  setValues(partial: Partial<AutomationFormValues>): void;
  submit(): void;
  reload(): void;
  armDelete(): void;
  confirmDelete(): void;
}

let handle: AutomationFormAutomationHandle | null = null;

export function setAutomationFormAutomationHandle(next: AutomationFormAutomationHandle | null): void {
  handle = next;
}

export function getAutomationFormAutomationHandle(): AutomationFormAutomationHandle | null {
  return handle;
}
