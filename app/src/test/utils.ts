
import {
  MockDaemon,
} from './mocks/daemon';

export interface RenderWithMockDaemonResult {
  mockDaemon: MockDaemon;
  [key: string]: unknown;
}

export function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

export * from '@testing-library/react';
export { default as userEvent } from '@testing-library/user-event';

export {
  MockDaemon,
  createMockDaemon,
  waitForCalls,
  assertNoMoreCalls,
} from './mocks/daemon';
