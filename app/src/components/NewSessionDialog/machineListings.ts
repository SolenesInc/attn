import { useCallback, useReducer, useRef } from 'react';
import type { BrowseDirectoryResult, InspectPathResult } from '../../hooks/useDaemonSocket';
import type { DirectoryEntry, PathInspection } from '../../types/generated';
import { normalizePickerPath } from '../../utils/locationPickerPaths';

export type Listing =
  | { status: 'pending'; started: number }
  | { status: 'listed'; directory: string; entries: DirectoryEntry[]; at: number; order: number }
  | { status: 'missing' }
  | { status: 'failed'; error: string };

export type Inspection =
  | { status: 'pending' }
  | { status: 'inspected'; inspection: PathInspection; order: number }
  | { status: 'failed' };

export type BrowseOn = (inputPath: string, extensions?: string[], host?: string) => Promise<BrowseDirectoryResult>;
export type InspectOn = (path: string, host?: string) => Promise<InspectPathResult>;

export function splitBrowsePath(path: string): { dir: string; fragment: string } {
  const slash = path.lastIndexOf('/');
  if (slash < 0) return { dir: '~', fragment: path.slice(1) };
  return { dir: path.slice(0, slash) || '/', fragment: path.slice(slash + 1) };
}

export function matchingFolders(entries: DirectoryEntry[], fragment: string): DirectoryEntry[] {
  const needle = fragment.toLowerCase();
  const starts = (entry: DirectoryEntry) => entry.name.toLowerCase().startsWith(needle);
  const folders = entries.filter(
    (entry) => entry.is_dir && entry.name.toLowerCase().includes(needle),
  );
  return [...folders.filter(starts), ...folders.filter((entry) => !starts(entry))];
}

export function useMachineListings(browse?: BrowseOn, inspect?: InspectOn) {
  const listings = useRef(new Map<string, Listing>());
  const inspections = useRef(new Map<string, Inspection>());
  const homes = useRef(new Map<string, string>());
  const epoch = useRef(0);
  const arrivals = useRef(0);
  const [version, changed] = useReducer((count: number) => count + 1, 0);

  const home = useCallback((host?: string) => homes.current.get(host ?? '') ?? '', []);
  const key = useCallback(
    (host: string | undefined, path: string, knownHome = home(host)) =>
      `${host ?? ''}\n${normalizePickerPath(path, knownHome)}`,
    [home],
  );
  const learnHome = useCallback((host: string | undefined, path?: string) => {
    if (!path || homes.current.get(host ?? '') === path) return;
    homes.current.set(host ?? '', path);
    changed();
  }, []);

  const reset = useCallback(() => {
    epoch.current++;
    listings.current.clear();
    inspections.current.clear();
    changed();
  }, []);

  const listing = useCallback(
    (host: string | undefined, dir: string) =>
      listings.current.get(key(host, dir)) ?? listings.current.get(key(host, dir, '')),
    [key],
  );

  const request = useCallback(
    (host: string | undefined, dir: string) => {
      const requested = key(host, dir);
      if (!browse || listing(host, dir)) return;
      const opening = epoch.current;
      const settle = (value: Listing, directory?: string) => {
        if (opening !== epoch.current) return;
        listings.current.set(requested, value);
        listings.current.set(key(host, dir), value);
        if (directory) listings.current.set(key(host, directory), value);
        changed();
      };
      listings.current.set(requested, { status: 'pending', started: Date.now() });
      changed();
      const missing = async (error: string): Promise<Listing> => {
        const there = await inspect?.(dir, host).catch(() => undefined);
        return there?.inspection && !there.inspection.exists
          ? { status: 'missing' }
          : { status: 'failed', error };
      };
      browse(dir === '/' ? '/' : `${dir.replace(/\/+$/, '')}/`, undefined, host)
        .then(async (reply) => {
          learnHome(host, reply.home_path);
          settle(
            reply.success
              ? {
                  status: 'listed',
                  directory: reply.directory,
                  entries: reply.entries || [],
                  at: Date.now(),
                  order: ++arrivals.current,
                }
              : await missing(reply.error || 'Listing failed'),
            reply.success ? reply.directory : undefined,
          );
        })
        .catch(async (reason: unknown) =>
          settle(await missing(reason instanceof Error ? reason.message : String(reason))),
        );
    },
    [browse, inspect, key, learnHome, listing],
  );

  const inspection = useCallback(
    (host: string | undefined, path: string) => inspections.current.get(`${host ?? ''}\n${path}`),
    [],
  );

  const requestInspection = useCallback(
    (host: string | undefined, path: string) => {
      const requested = `${host ?? ''}\n${path}`;
      if (!inspect || inspections.current.has(requested)) return;
      const opening = epoch.current;
      inspections.current.set(requested, { status: 'pending' });
      const settle = (value: Inspection) => {
        if (opening !== epoch.current) return;
        inspections.current.set(requested, value);
        changed();
      };
      inspect(path, host)
        .then((reply) => {
          learnHome(host, reply.inspection?.home_path);
          settle(
            reply.success && reply.inspection
              ? { status: 'inspected', inspection: reply.inspection, order: ++arrivals.current }
              : { status: 'failed' },
          );
        })
        .catch(() => settle({ status: 'failed' }));
    },
    [inspect, learnHome],
  );

  const known = useCallback(
    (host: string | undefined, path: string) => {
      const target = normalizePickerPath(path, home(host));
      const parent = target.slice(0, target.lastIndexOf('/')) || '/';
      const found = listing(host, parent);
      if (found?.status !== 'listed') return undefined;
      const entry = found.entries.find((candidate) => candidate.path === target);
      return entry ? entry.is_repo === true : undefined;
    },
    [home, listing],
  );

  return { version, reset, home, learnHome, listing, request, inspection, requestInspection, known };
}
