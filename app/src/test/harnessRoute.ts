import { fireEvent, screen, within } from '@testing-library/react';
import { gesture } from './renderApp';
import type { ScriptedDaemon } from './scriptedDaemon';

export const routeDialog = () => within(screen.getByRole('dialog', { name: 'Choose a model' }));
export async function openRoute(daemon: ScriptedDaemon, label: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: label })));
}
export async function previewHarness(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(within(routeDialog().getByRole('listbox', { name: 'Harness' })).getByRole('option', { name: new RegExp(`^${name}`) })));
}
export async function pickModel(daemon: ScriptedDaemon, name: string | RegExp) {
  await gesture(daemon, () => fireEvent.click(within(routeDialog().getByRole('listbox', { name: 'Model' })).getByRole('option', { name: typeof name === 'string' ? new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`) : name })));
}
export async function pickEffort(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(within(routeDialog().getByRole('group', { name: 'Effort' })).getByRole('button', { name })));
}
export async function enterModel(daemon: ScriptedDaemon, id: string, provider?: string) {
  await gesture(daemon, () => fireEvent.change(routeDialog().getByRole('textbox', { name: 'Filter models or enter an ID' }), { target: { value: id } }));
  if (provider) fireEvent.change(routeDialog().getByRole('textbox', { name: 'Provider' }), { target: { value: provider } });
  await gesture(daemon, () => fireEvent.click(routeDialog().getByRole('button', { name: `Use “${id}”` })));
}
export async function setRouteEffort(daemon: ScriptedDaemon, label: string, effort: string) {
  if (!screen.queryByRole('dialog', { name: 'Choose a model' })) await openRoute(daemon, label);
  const group = routeDialog().queryByRole('group', { name: 'Effort' });
  const option = group ? within(group).queryByRole('button', { name: effort }) : null;
  if (option) await gesture(daemon, () => fireEvent.click(option));
  else {
    const field = routeDialog().getByRole('textbox', { name: 'Effort' });
    field.focus(); fireEvent.change(field, { target: { value: effort } });
    await gesture(daemon, () => fireEvent.blur(field));
  }
}
