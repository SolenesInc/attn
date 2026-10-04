
import {
  MockDaemon,
  createFileDiffResult,
} from './mocks/daemon';

export interface RenderWithMockDaemonResult {
  mockDaemon: MockDaemon;
  [key: string]: unknown;
}

export function setupDefaultResponses(mockDaemon: MockDaemon): void {
  mockDaemon.setResponse('fetchDiff', (args: unknown[]) => {
    const [path] = args as [string, { staged?: boolean; baseRef?: string }];
    return {
      ...createFileDiffResult('// original content', '// modified content'),
      path,
    };
  });

  mockDaemon.setResponse('fetchRemotes', () => ({ success: true }));
}

export function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

export * from '@testing-library/react';
export { default as userEvent } from '@testing-library/user-event';

export {
  MockDaemon,
  createMockDaemon,
  createGitStatus,
  createFileDiffResult,
  waitForCalls,
  assertNoMoreCalls,
} from './mocks/daemon';
