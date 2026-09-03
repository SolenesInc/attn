#!/usr/bin/env node


import { currentHarnessProfile } from './harnessProfile.mjs';
import { ensureMockGitHubServer, mockGitHubTarget, readMockGitHubStatus, stopMockGitHubServer } from './mockGitHub.mjs';

const command = process.argv[2] || 'status';
const profile = currentHarnessProfile();

if (command === 'ensure') {
  console.log(JSON.stringify(ensureMockGitHubServer({ profile }), null, 2));
} else if (command === 'stop') {
  console.log(JSON.stringify(stopMockGitHubServer({ profile }), null, 2));
} else if (command === 'status') {
  const target = mockGitHubTarget(profile);
  try {
    const status = await readMockGitHubStatus({ profile });
    console.log(JSON.stringify({ ...target, running: true, pid: status.pid, prs: status.prs, requestCount: status.requestCount }, null, 2));
  } catch {
    console.log(JSON.stringify({ ...target, running: false }, null, 2));
  }
} else {
  console.error(`Unknown command ${JSON.stringify(command)}; expected status, ensure or stop.`);
  process.exitCode = 2;
}
