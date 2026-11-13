#!/usr/bin/env node

import path from 'node:path';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-DESKTOP-ORDER', tier: 'tier1-local-shell', prefix: 'native-desktop-order',
  metadata: { focus: 'In the native app Sort desktops from the palette puts numbered desktops first and named ones by name, and the Undo its notice offers puts back the order before, without the app taking focus' },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

const collator = new Intl.Collator('en', { sensitivity: 'base', numeric: true });

function sortedOrder(desktops) {
  const slot = (d) => (d.shortcut_slot > 0 ? d.shortcut_slot : null);
  const name = (d) => (d.name ?? '').trim();
  return [...desktops].sort((a, b) => {
    if (slot(a) && slot(b)) return slot(a) - slot(b);
    if (slot(a) || slot(b)) return slot(a) ? -1 : 1;
    if (!name(a) || !name(b)) return name(a) ? -1 : name(b) ? 1 : 0;
    return collator.compare(name(a), name(b));
  }).map((d) => d.id);
}

function sidebarOrder(node, ids, found = []) {
  const id = node?.key?.startsWith('sidebar.desktop.') ? node.key.slice('sidebar.desktop.'.length) : null;
  if (id && ids.includes(id)) found.push(id);
  for (const child of node?.children ?? []) sidebarOrder(child, ids, found);
  return found;
}

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

async function arrangedAs(ids, description) {
  await observer.waitFor(() => same(observer.desktops.map((d) => d.id), ids), description);
  const shown = sidebarOrder(await driver.tree(), ids);
  runner.assert(same(shown, ids), `the sidebar lists the desktops ${description}`, { shown, ids });
}

try {
  const checksFocus = process.platform === 'darwin';
  const frontmostBefore = checksFocus ? await driver.frontmostBundleId() : null;
  await runner.step('launch_app', () => launchFreshNativeAppAndConnect(client, observer));

  let before;
  let sorted;
  await runner.step('arrange_desktops_by_hand', async () => {
    const create = async (slot, name) => {
      const result = await observer.profileCommand('desktop_create', { profile_id: observer.profileId, shortcut_slot: slot, name });
      const id = result.desktops[0].id;
      runner.registerCleanup(`close_${name}`, () => {
        const left = observer.desktop(id);
        return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
      });
      return id;
    };
    const six = await create(6, 'Six');
    const nine = await create(9, 'Nine');
    const named10 = await create(0, 'Review 10');
    const named2 = await create(0, 'review 2');
    const alpha = await create(0, 'Alpha');
    const others = observer.desktops.map((d) => d.id).filter((id) => ![six, nine, named10, named2, alpha].includes(id));
    before = [nine, named10, ...others, six, named2, alpha];
    await observer.profileCommand('desktop_set_order', { profile_id: observer.profileId, desktop_ids: before });
    await arrangedAs(before, 'in the order set by hand');
    sorted = sortedOrder(observer.desktops);
    runner.assert(!same(sorted, before), 'the hand order is not already sorted', { before, sorted });
  });

  await runner.step('sort_from_palette', async () => {
    await driver.run('ui.commandPalette');
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'Sort desktops');
    await driver.waitFor({ key: 'palette.row.command:sort-desktops' });
    await driver.pressKey('Enter');
    await driver.waitFor({ key: 'palette' }, { gone: true });
    await arrangedAs(sorted, 'numbered first, then by name');
    const offer = await driver.waitFor({ key: 'notice.offer' });
    await driver.hover({ key: 'notice.offer' });
    runner.assert(offer.name === 'Desktops sorted Undo', 'the notice offers Undo', offer);
    await driver.screenshot(path.join(runner.runDir, 'sorted.png'));
  });

  await runner.step('undo_restores_order', async () => {
    await driver.click({ key: 'notice.offer' });
    await arrangedAs(before, 'back in the order before the sort');
    const done = await driver.waitFor({ key: 'notice.offer' });
    runner.assert(done.name === 'Desktops sorted ✓ Done', 'the notice says Undo is done', done);
    await driver.screenshot(path.join(runner.runDir, 'undone.png'));
  });

  const frontmostAfter = checksFocus ? await driver.frontmostBundleId() : null;
  runner.assert(frontmostAfter === frontmostBefore, 'the app took no focus', { frontmostBefore, frontmostAfter });

  await runner.finishSuccess({ before, sorted, frontmostBefore, frontmostAfter });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
