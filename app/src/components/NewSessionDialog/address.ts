import type { OutpostInfo } from '../../types/generated';

export type Machine = { host?: string; name: string; label: string };
export const THIS_MAC: Machine = { name: 'This Mac', label: 'home' };

export type Address =
  | { mode: 'search'; text: string; prefix: string; machine?: Machine }
  | { mode: 'browse'; path: string; prefix: string; machine: Machine }
  | { mode: 'ambiguous'; prefix: string; machines: Machine[] };

export function isBrowsePath(text: string): boolean {
  const trimmed = text.trim();
  return trimmed.startsWith('~') || trimmed.startsWith('/');
}

function sharesName(outpost: OutpostInfo, outposts: OutpostInfo[]): boolean {
  const lower = outpost.name.toLowerCase();
  return outposts.some((other) => other.id !== outpost.id && other.name.toLowerCase() === lower);
}

export function machineFor(outpost: OutpostInfo, outposts: OutpostInfo[]): Machine {
  return {
    host: outpost.id,
    name: outpost.name,
    label: sharesName(outpost, outposts) ? outpost.id : outpost.name,
  };
}

function named(query: string, outposts: OutpostInfo[]): { machines: Machine[]; prefix: string } | null {
  const lower = query.toLowerCase();
  const matches = [
    ...outposts.flatMap((outpost) =>
      [outpost.name, outpost.id].map((label) => ({ label, machine: machineFor(outpost, outposts) })),
    ),
    { label: THIS_MAC.label, machine: THIS_MAC },
  ].filter(({ label }) => lower.startsWith(`${label.toLowerCase()}:`));
  if (!matches.length) return null;
  const longest = Math.max(...matches.map(({ label }) => label.length));
  const machines = new Map(
    matches
      .filter(({ label }) => label.length === longest)
      .map(({ machine }) => [machine.host ?? '', machine]),
  );
  return { machines: [...machines.values()], prefix: query.slice(0, longest + 1) };
}

export function parseAddress(query: string, outposts: OutpostInfo[]): Address {
  const addressed = named(query, outposts);
  if (addressed && addressed.machines.length > 1)
    return { mode: 'ambiguous', prefix: addressed.prefix, machines: addressed.machines };
  const machine = addressed?.machines[0];
  const rest = addressed ? query.slice(addressed.prefix.length) : query;
  const prefix = addressed?.prefix ?? '';
  if (isBrowsePath(rest)) return { mode: 'browse', path: rest.trim(), prefix, machine: machine ?? THIS_MAC };
  return { mode: 'search', text: rest.trim(), prefix, machine };
}

export function machinesNamed(text: string, outposts: OutpostInfo[]): OutpostInfo[] {
  const lower = text.trim().toLowerCase();
  if (!lower || lower.includes(':')) return [];
  return outposts.filter((outpost) => outpost.name.toLowerCase().startsWith(lower));
}

export function addressOf(machine: Machine, path: string): string {
  return machine.host ? `${machine.label}:${path}` : path;
}
