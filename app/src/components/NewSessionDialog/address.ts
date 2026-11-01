import type { OutpostInfo } from '../../types/generated';

export type Machine = { host?: string; name: string };
export const THIS_MAC: Machine = { name: 'This Mac' };
const HOME_NAME = 'home';

export type Address =
  | { mode: 'search'; text: string; prefix: string; machine?: Machine }
  | { mode: 'browse'; path: string; prefix: string; machine: Machine };

export function isBrowsePath(text: string): boolean {
  const trimmed = text.trim();
  return trimmed.startsWith('~') || trimmed.startsWith('/');
}

function named(query: string, outposts: OutpostInfo[]): { machine: Machine; prefix: string } | null {
  const lower = query.toLowerCase();
  const candidates = [
    ...outposts.map((outpost) => ({ name: outpost.name, machine: { host: outpost.id, name: outpost.name } })),
    { name: HOME_NAME, machine: THIS_MAC },
  ].sort((a, b) => b.name.length - a.name.length);
  for (const candidate of candidates) {
    const prefix = `${candidate.name}:`;
    if (lower.startsWith(prefix.toLowerCase()))
      return { machine: candidate.machine, prefix: query.slice(0, prefix.length) };
  }
  return null;
}

export function parseAddress(query: string, outposts: OutpostInfo[]): Address {
  const addressed = named(query, outposts);
  const rest = addressed ? query.slice(addressed.prefix.length) : query;
  const prefix = addressed?.prefix ?? '';
  if (isBrowsePath(rest))
    return { mode: 'browse', path: rest.trim(), prefix, machine: addressed?.machine ?? THIS_MAC };
  return { mode: 'search', text: rest.trim(), prefix, machine: addressed?.machine };
}

export function machinesNamed(text: string, outposts: OutpostInfo[]): OutpostInfo[] {
  const lower = text.trim().toLowerCase();
  if (!lower || lower.includes(':')) return [];
  return outposts.filter((outpost) => outpost.name.toLowerCase().startsWith(lower));
}

export function addressOf(machine: Machine, path: string): string {
  return machine.host ? `${machine.name}:${path}` : path;
}
