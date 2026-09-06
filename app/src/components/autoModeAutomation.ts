import type { AutoModeEnvironmentSlotValue } from '../types/generated';

export interface AutoModeAutomationState {
  present: boolean;
  enabledDefault: boolean;
  approvalPolicy: string;
  sandboxMode: string;
  rules: string[];
  allowedHosts: string[];
  deniedHosts: string[];
  legacyPatterns: string[];
  proposals: number;
  environment: {
    slots: AutoModeEnvironmentSlotValue[];
    notes: string[];
    filled: number;
    total: number;
    editing: string;
    saving: boolean;
    error: string;
  };
}

export interface AutoModeAutomationHandle {
  getState(): AutoModeAutomationState;
  setEnvironmentSlot(id: string, values: string[]): Promise<void>;
}

let handle: AutoModeAutomationHandle | null = null;

export function setAutoModeAutomationHandle(next: AutoModeAutomationHandle | null): void {
  handle = next;
}

export function getAutoModeAutomationHandle(): AutoModeAutomationHandle | null {
  return handle;
}

export const INACTIVE_AUTOMODE_STATE: AutoModeAutomationState = {
  present: false,
  enabledDefault: false,
  approvalPolicy: '',
  sandboxMode: '',
  rules: [],
  allowedHosts: [],
  deniedHosts: [],
  legacyPatterns: [],
  proposals: 0,
  environment: {
    slots: [],
    notes: [],
    filled: 0,
    total: 0,
    editing: '',
    saving: false,
    error: '',
  },
};
