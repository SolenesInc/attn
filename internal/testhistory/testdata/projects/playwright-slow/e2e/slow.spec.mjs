import { test } from '@playwright/test';
test('finishes first', async () => {});
test('is interrupted', async () => { test.setTimeout(60_000); await new Promise(r => setTimeout(r, 30_000)); });
test('never starts', async () => {});
