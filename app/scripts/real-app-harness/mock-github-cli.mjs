#!/usr/bin/env node


import { currentHarnessInstance } from './harnessInstance.mjs';
import { ensureMockGitHubServer, mockGitHubTarget, readMockGitHubStatus, stopMockGitHubServer } from './mockGitHub.mjs';

const command = process.argv[2] || 'status';
const instance = currentHarnessInstance();

if (command === 'ensure') {
  console.log(JSON.stringify(ensureMockGitHubServer({ instance }), null, 2));
} else if (command === 'stop') {
  console.log(JSON.stringify(stopMockGitHubServer({ instance }), null, 2));
} else if (command === 'status') {
  const target = mockGitHubTarget(instance);
  try {
    const status = await readMockGitHubStatus({ instance });
    console.log(JSON.stringify({ ...target, running: true, pid: status.pid, prs: status.prs, requestCount: status.requestCount }, null, 2));
  } catch {
    console.log(JSON.stringify({ ...target, running: false }, null, 2));
  }
} else {
  console.error(`Unknown command ${JSON.stringify(command)}; expected status, ensure or stop.`);
  process.exitCode = 2;
}
