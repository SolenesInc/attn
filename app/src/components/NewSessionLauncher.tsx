import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { LocationPickerProps } from './LocationPicker';
import type { RecentLocation } from '../hooks/useDaemonSocket';
import type { OutpostInfo } from '../types/generated';
import type { SessionAgent } from '../types/sessionAgent';
import { useSettings } from '../contexts/SettingsContext';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { agentLabel, isAgentSelectable, type HarnessAvailability } from '../utils/agentAvailability';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { PathInput } from './NewSessionDialog/PathInput';
import {
  SessionControls,
  useSessionControls,
  type MissingHarnesses,
} from './NewSessionDialog/SessionControls';
import {
  parseRepoDestination,
  repoDestinationSettingKey,
} from './NewSessionDialog/destinationMemory';
import { generateWorktreeName } from './NewSessionDialog/worktreeNames';
import { LauncherIcon } from './NewSessionDialog/LauncherIcon';
import {
  THIS_MAC,
  addressOf,
  machineFor,
  machinesNamed,
  parseAddress,
  type Machine,
} from './NewSessionDialog/address';
import {
  matchingFolders,
  splitBrowsePath,
  useMachineListings,
  type BrowseOn,
  type InspectOn,
} from './NewSessionDialog/machineListings';
import { WhereList, locationAge, type WhereRow } from './NewSessionDialog/WhereList';
import {
  expandDisplayPath,
  normalizePickerPath,
  pathBasename,
  toDisplayPath,
} from '../utils/locationPickerPaths';
import { isAccelKeyPressed } from '../shortcuts/platform';
import { keyCombo } from '../shortcuts/formatShortcut';
import './LocationPicker.css';
import './NewSessionLauncher.css';

type RepoInfo = NonNullable<
  Awaited<ReturnType<NonNullable<LocationPickerProps['onGetRepoInfo']>>>['info']
>;
type Row = {
  kind: 'recent' | 'folder' | 'machine' | 'elsewhere';
  path: string;
  name: string;
  host?: string;
  repoRoot?: string;
  lastSeen?: string;
};
type Problem = { message: string; reason?: string };
type Placement = { host?: string; desktopId?: string };
export interface NewSessionLauncherProps extends Omit<
  LocationPickerProps,
  | 'onSelect'
  | 'onCreateWorktreeSession'
  | 'projectsDirectory'
  | 'onGetRecentLocations'
  | 'onGetRepoInfo'
  | 'onDeleteWorktree'
  | 'onBrowseDirectory'
  | 'onInspectPath'
> {
  desktopId?: string;
  onBrowseDirectory?: BrowseOn;
  onInspectPath?: InspectOn;
  onSelect: (
    path: string,
    agent: SessionAgent,
    yolo: boolean,
    chief: boolean,
    placement: Placement,
  ) => Promise<unknown>;
  onCreateWorktreeSession: (
    repo: string,
    branch: string,
    from: string,
    agent: SessionAgent,
    yolo: boolean,
    chief: boolean | undefined,
    options: Placement & { onCreated?: (path: string) => void },
  ) => Promise<unknown>;
  onGetRecentLocations?: (
    limit?: number,
    machines?: 'home' | 'every',
  ) => Promise<{ locations: RecentLocation[]; home_path?: string }>;
  onGetRepoInfo?: (
    repo: string,
    host?: string,
  ) => ReturnType<NonNullable<LocationPickerProps['onGetRepoInfo']>>;
  onDeleteWorktree?: (
    path: string,
    options?: { force?: boolean },
    host?: string,
  ) => Promise<{ success: boolean; error?: string }>;
}
type Where = { repo: string; host?: string; info?: RepoInfo };

const NO_OUTPOSTS: OutpostInfo[] = [];

function listedAge(at: number): string {
  const age = locationAge(new Date(at).toISOString());
  return age === 'now' ? 'just now' : `${age} ago`;
}

function unreachableReason(outpost?: OutpostInfo): string {
  if (!outpost) return 'It is no longer attached to this Mac.';
  if (outpost.state === 'refused') return outpost.last_error || 'It refused the connection.';
  if (outpost.state === 'lost') {
    const age = locationAge(outpost.state_since);
    return age === 'now' ? 'Lost just now.' : `Lost for ${age}.`;
  }
  return 'Still connecting.';
}

export function NewSessionLauncher({
  isOpen,
  initialAgent,
  variant = 'dialog',
  active = true,
  title = 'New agent',
  onClose,
  onSelect,
  onGetRecentLocations,
  onBrowseDirectory,
  onInspectPath,
  onGetRepoInfo,
  onCreateWorktreeSession,
  onDeleteWorktree,
  onError,
  desktopId,
  agentAvailability,
  chiefExists,
}: NewSessionLauncherProps) {
  const inline = variant === 'inline';
  const { settings, setSetting } = useSettings();
  const api = useOptionalDaemonApi();
  const outposts = (settings.outposts_enabled === 'true' && api?.outposts) || NO_OUTPOSTS;
  const outpostsAtOpen = useRef(outposts);
  outpostsAtOpen.current = outposts;
  const sendAgentCatalog = api?.sendAgentCatalog;
  const [catalogs, setCatalogs] = useState<Record<string, ReadonlySet<string>>>({});
  const installedHere = useMemo(() => {
    const availability = agentAvailability ?? {};
    return new Set(Object.keys(availability).filter((id) => isAgentSelectable(availability, id)));
  }, [agentAvailability]);
  const anyMachineAvailability = useMemo(() => {
    const remote = Object.values(catalogs)
      .flatMap((installed) => [...installed])
      .filter((id) => agentAvailability?.[id] !== 'checking');
    if (!agentAvailability || !remote.length) return agentAvailability;
    return { ...agentAvailability, ...Object.fromEntries(remote.map((id): [string, HarnessAvailability] => [id, 'available'])) };
  }, [agentAvailability, catalogs]);
  const controls = useSessionControls({
    isOpen,
    initialAgent,
    agentAvailability: anyMachineAvailability,
    chiefExists,
    resetAgentOnOpen: true,
  });
  const [query, setQuery] = useState('');
  const [highlight, setHighlight] = useState(-1);
  const [recents, setRecents] = useState<RecentLocation[]>([]);
  const [fresh, setFresh] = useState(false);
  const listings = useMachineListings(onBrowseDirectory, onInspectPath);
  const home = listings.home();
  const [where, setWhere] = useState<Where | null>(null);
  const [branch, setBranch] = useState('');
  const [error, setError] = useState<Problem | null>(null);
  const [busy, setBusy] = useState(false);
  const [starting, setStarting] = useState<{ host?: string; at: number } | null>(null);
  const run = useRef<{ background: boolean } | null>(null);
  const [, tick] = useState(0);
  const [deletion, setDeletion] = useState<{
    path: string;
    branch: string;
    force: boolean;
  } | null>(null);
  const generation = useRef(0);
  const generated = useRef(new Map<string, string>());
  const created = useRef(new Map<string, string>());
  const root = useRef<HTMLDivElement>(null);
  const address = parseAddress(query, outposts);
  const browsing = address.mode === 'browse' ? address : null;
  const pathMode = Boolean(browsing);
  const browseHost = browsing?.machine.host;
  const browseDir = browsing ? splitBrowsePath(browsing.path) : null;
  const remember = (repo: string, host?: string) =>
    parseRepoDestination(settings[repoDestinationSettingKey(repo, host)]) || 'main_repo';
  const display = (path: string, host?: string) => toDisplayPath(path, listings.home(host));
  const outpostOf = (host: string) => outposts.find((outpost) => outpost.id === host);
  const machineName = (host?: string) => (host ? (outpostOf(host)?.name ?? host) : THIS_MAC.name);
  const machineOf = (host?: string): Machine => {
    if (!host) return THIS_MAC;
    const outpost = outpostOf(host);
    return outpost ? machineFor(outpost, outposts) : { host, name: host, label: host };
  };
  const reachable = (host?: string) => !host || outpostOf(host)?.reach === 'reachable';
  const onMachine = (repo: string, host?: string) => (host ? `${host}:${repo}` : repo);
  const fail = (reason: unknown) =>
    setError({ message: reason instanceof Error ? reason.message : String(reason) });
  const installedOn = (host?: string): ReadonlySet<string> | undefined =>
    host ? catalogs[host] : Object.keys(catalogs).length ? installedHere : undefined;
  const refusal = (host: string | undefined, agent: SessionAgent): Problem | null => {
    const name = machineName(host);
    if (host) {
      const outpost = outpostOf(host);
      if (outpost?.reach !== 'reachable')
        return {
          message: `${name} is unreachable; this needs it now`,
          reason: unreachableReason(outpost),
        };
    }
    const installed = installedOn(host);
    if (agent !== 'shell' && installed && !installed.has(agent))
      return {
        message: `${agentLabel(agent)} is not installed on ${name}. Pick another harness (${keyCombo('alt', '1')}–${keyCombo('alt', '9')}) or machine.`,
      };
    return null;
  };

  const current = browseDir ? listings.listing(browseHost, browseDir.dir) : undefined;
  const currentDir =
    current?.status === 'listed' ? display(current.directory, browseHost) : '';
  const browseReachable = reachable(browseHost);
  const browsedDir = browseDir?.dir;
  useEffect(() => {
    if (isOpen && !where && browsedDir !== undefined && browseReachable)
      listings.request(browseHost, browsedDir);
  }, [isOpen, where, browseHost, browsedDir, browseReachable, listings.request]);
  const missingHere = outposts.length > 0 && current?.status === 'missing';
  const elsewhereMachines = missingHere
    ? [undefined, ...outposts.map((outpost) => outpost.id)].filter(
        (host) => host !== browseHost && reachable(host),
      )
    : [];
  const elsewhereKey = elsewhereMachines.map((host) => host ?? THIS_MAC.name).join('\n');
  useEffect(() => {
    if (!isOpen || browsedDir === undefined) return;
    for (const host of elsewhereMachines) listings.request(host, browsedDir);
  }, [isOpen, browsedDir, elsewhereKey, listings.request]); // eslint-disable-line react-hooks/exhaustive-deps
  const aimed = address.mode === 'search' && address.machine ? address : null;
  const aimedNeedle = aimed?.text.toLowerCase() ?? '';
  const aimedCandidates = aimed
    ? recents.filter(
        (item) =>
          (item.host || undefined) !== aimed.machine!.host &&
          item.path.toLowerCase().includes(aimedNeedle),
      )
    : [];
  const askedPath = (item: RecentLocation) => {
    const there = listings.home(item.host || undefined);
    return there ? toDisplayPath(item.path, there) : item.path;
  };
  const aimedHost = aimed && reachable(aimed.machine!.host) ? (aimed.machine!.host ?? '') : null;
  const askedPaths = aimedCandidates.map(askedPath);
  const askedKey = askedPaths.join('\n');
  useEffect(() => {
    if (!isOpen || aimedHost === null) return;
    for (const path of askedPaths) listings.requestInspection(aimedHost || undefined, path);
  }, [isOpen, aimedHost, askedKey, listings.requestInspection]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    generation.current++;
    setFresh(false);
    setQuery('');
    if (!isOpen) return;
    setHighlight(-1);
    setWhere(null);
    setBranch('');
    setError(null);
    setDeletion(null);
    setBusy(false);
    setStarting(null);
    setCatalogs({});
    run.current = null;
    generated.current.clear();
    created.current.clear();
    listings.reset();
    let cancelled = false;
    const attached = outpostsAtOpen.current;
    for (const outpost of attached) listings.learnHome(outpost.id, outpost.home);
    for (const outpost of attached)
      if (outpost.reach === 'reachable')
        sendAgentCatalog?.(outpost.id)
          .then((catalog) => {
            if (cancelled) return;
            const installed = new Set(
              catalog.harnesses.filter((harness) => harness.available).map(({ id }) => id),
            );
            setCatalogs((current) => ({ ...current, [outpost.id]: installed }));
          })
          .catch(() => {});
    onGetRecentLocations?.(undefined, attached.length ? 'every' : 'home')
      .then((reply) => {
        if (cancelled) return;
        setRecents(
          [...reply.locations].sort((a, b) => b.last_seen.localeCompare(a.last_seen)),
        );
        listings.learnHome(undefined, reply.home_path);
        setFresh(true);
        for (const outpost of attached) {
          if (outpost.reach !== 'reachable') continue;
          listings.request(outpost.id, '~');
          for (const item of reply.locations)
            if (item.host === outpost.id)
              listings.request(outpost.id, item.path.replace(/\/+$/, '').replace(/\/[^/]*$/, '') || '/');
        }
      })
      .catch((reason) => {
        if (!cancelled) fail(reason);
      });
    return () => {
      cancelled = true;
      generation.current++;
    };
  }, [isOpen, onGetRecentLocations, sendAgentCatalog]); // eslint-disable-line react-hooks/exhaustive-deps

  const rows = useMemo<Row[]>(() => {
    const recentRow = (item: RecentLocation): Row => ({
      kind: 'recent',
      path: item.path,
      name: pathBasename(item.path),
      host: item.host || undefined,
      repoRoot: item.repo_root,
      lastSeen: item.last_seen,
    });
    if (browsing && browseDir) {
      const needle = expandDisplayPath(browsing.path, listings.home(browseHost)).toLowerCase();
      const recentRows = recents
        .filter(
          (item) =>
            (item.host || undefined) === browseHost &&
            item.path.toLowerCase().startsWith(needle) &&
            item.path.replace(/\/+$/, '').toLowerCase() !== needle.replace(/\/+$/, ''),
        )
        .map(recentRow);
      const folderRows: Row[] =
        current?.status === 'listed'
          ? matchingFolders(current.entries, browseDir.fragment).map((entry) => ({
              kind: 'folder',
              path: entry.path,
              name: entry.name,
              host: browseHost,
              repoRoot: entry.is_repo ? entry.path : undefined,
            }))
          : [];
      const elsewhereRows: Row[] = elsewhereMachines
        .flatMap((host) => {
          const there = listings.listing(host, browseDir.dir);
          return there?.status === 'listed' ? [{ host, order: there.order }] : [];
        })
        .sort((a, b) => a.order - b.order)
        .map(({ host }) => ({
          kind: 'elsewhere',
          path: browseDir.dir,
          name: `${browseDir.dir}/ is on ${machineName(host)}`,
          host,
        }));
      return [...recentRows, ...folderRows, ...elsewhereRows];
    }
    if (aimed) {
      const host = aimed.machine!.host;
      const own = recents
        .filter(
          (item) =>
            (item.host || undefined) === host && item.path.toLowerCase().includes(aimedNeedle),
        )
        .map(recentRow);
      const seen = new Set(own.map((row) => row.path));
      const confirmed = aimedCandidates
        .flatMap((item) => {
          const found = listings.inspection(host, askedPath(item));
          return found?.status === 'inspected' &&
            found.inspection.exists &&
            found.inspection.is_directory
            ? [{ item, inspection: found.inspection, order: found.order }]
            : [];
        })
        .sort((a, b) => a.order - b.order)
        .flatMap(({ item, inspection }): Row[] => {
          if (seen.has(inspection.resolved_path)) return [];
          seen.add(inspection.resolved_path);
          return [
            {
              kind: 'recent',
              path: inspection.resolved_path,
              name: pathBasename(inspection.resolved_path),
              host,
              repoRoot:
                inspection.repo_root === inspection.resolved_path ? inspection.repo_root : undefined,
              lastSeen: item.last_seen,
            },
          ];
        });
      return [...own, ...confirmed];
    }
    const searched = address.mode === 'search' ? address.text : '';
    const needle = expandDisplayPath(searched, home).toLowerCase();
    const machineRows: Row[] = (
      address.mode === 'ambiguous'
        ? address.machines.flatMap((machine) => outposts.filter(({ id }) => id === machine.host))
        : machinesNamed(searched, outposts)
    ).map((outpost) => ({
      kind: 'machine',
      path: '~/',
      name: `${outpost.name}:`,
      host: outpost.id,
    }));
    if (address.mode === 'ambiguous') return machineRows;
    const recentRows = recents
      .filter((item) =>
        (item.host ? `${machineName(item.host)}:${item.path}` : item.path)
          .toLowerCase()
          .includes(needle),
      )
      .map(recentRow);
    return [...machineRows, ...recentRows];
  }, [query, recents, outposts, listings.version]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (where || pathMode) return;
    setHighlight((index) =>
      fresh && rows.length ? (index >= 0 && index < rows.length ? index : 0) : -1,
    );
  }, [fresh, rows, where, pathMode]);
  useEffect(() => {
    root.current
      ?.querySelector(`[data-index="${highlight}"]`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [highlight, query, branch, where?.info]);

  const name = where
    ? branch.trim() || generated.current.get(onMachine(where.repo, where.host)) || ''
    : '';
  const whereRows = useMemo<WhereRow[]>(() => {
    if (!where?.info) return [];
    const info = where.info;
    return [
      { kind: 'new', from: 'default', branch: info.default_branch } as const,
      ...(info.current_branch !== info.default_branch
        ? [
            {
              kind: 'new',
              from: 'current',
              branch: info.current_branch,
            } as const,
          ]
        : []),
      { kind: 'main', branch: info.current_branch } as const,
      ...info.worktrees
        .filter(
          (item) =>
            item.path !== where.repo &&
            item.branch.toLowerCase().includes(branch.trim().toLowerCase()),
        )
        .map((item) => ({
          kind: 'existing' as const,
          path: item.path,
          branch: item.branch,
          createdAt: item.created_at,
        })),
    ];
  }, [where, branch]);
  const memory = where ? remember(where.repo, where.host) : 'main_repo';
  const targetHost = where
    ? where.host
    : rows[highlight]
      ? rows[highlight].host
      : (browsing?.machine.host ?? aimed?.machine?.host);
  const targetCatalog = installedOn(targetHost);
  const missing: MissingHarnesses | undefined = targetCatalog && {
    machine: machineName(targetHost),
    harnesses: new Set(
      controls.orderedAgentList.filter(
        (candidate) => candidate !== 'shell' && !targetCatalog.has(candidate),
      ),
    ),
  };
  const startingOutpost = starting?.host ? outpostOf(starting.host) : undefined;
  const stalled = busy && startingOutpost?.state === 'stalled';
  const stalledSeconds =
    stalled && starting
      ? Math.max(
          0,
          Math.round(
            (Date.now() - Math.max(starting.at, Date.parse(startingOutpost.state_since) || 0)) /
              1000,
          ),
        )
      : 0;
  useEffect(() => {
    if (!stalled) return;
    const timer = window.setInterval(() => tick((count) => count + 1), 1000);
    return () => window.clearInterval(timer);
  }, [stalled]);
  const asking =
    isOpen && !where && Boolean(browseHost) && browseReachable && current?.status === 'pending';
  useEffect(() => {
    if (!asking) return;
    const timer = window.setInterval(() => tick((count) => count + 1), 100);
    return () => window.clearInterval(timer);
  }, [asking]);
  const background = () => {
    if (run.current) run.current.background = true;
    run.current = null;
    setBusy(false);
    setStarting(null);
    onClose();
  };
  useLayoutEffect(() => {
    if (!where?.info) return;
    const exact = branch.trim()
      ? whereRows.findIndex((row) => row.kind === 'existing' && row.branch === branch.trim())
      : -1;
    setHighlight(
      branch.trim()
        ? Math.max(0, exact)
        : memory === 'new_worktree'
          ? 0
          : whereRows.findIndex((row) => row.kind === 'main'),
    );
  }, [branch, where?.repo, Boolean(where?.info), memory]); // eslint-disable-line react-hooks/exhaustive-deps

  const changeQuery = (value: string) => {
    generation.current++;
    setQuery(value);
    setHighlight(parseAddress(value, outposts).mode === 'browse' ? -1 : 0);
    setError(null);
  };
  const stepBack = () => {
    if (busy) {
      if (stalled) background();
      return;
    }
    setError(null);
    if (deletion) {
      setDeletion(null);
      return;
    }
    generation.current++;
    if (where) {
      setHighlight(0);
      setWhere(null);
      setBranch('');
    } else if (query) {
      setQuery('');
      setHighlight(fresh && recents.length ? 0 : -1);
    } else if (!inline) onClose();
  };
  useEscapeStack(stepBack, isOpen && active && !inline);

  async function openWhere(repo: string, host?: string) {
    const refused = refusal(host, 'shell');
    if (refused) {
      setError(refused);
      return;
    }
    const request = ++generation.current;
    setWhere({ repo, host });
    setBranch('');
    setHighlight(-1);
    setError(null);
    try {
      const reply = await onGetRepoInfo?.(repo, host);
      if (request !== generation.current) return;
      if (!reply?.success || !reply.info)
        throw new Error(reply?.error || 'Failed to read repository');
      if (!generated.current.has(onMachine(repo, host)))
        generated.current.set(
          onMachine(repo, host),
          generateWorktreeName(reply.info.worktrees.map((item) => item.branch)),
        );
      setWhere((value) => value && { ...value, info: reply.info });
    } catch (reason) {
      if (request === generation.current) fail(reason);
    }
  }

  async function launch(row?: Row, terminal = false, destination?: WhereRow) {
    if (busy) return;
    if (row?.kind === 'machine' || row?.kind === 'elsewhere') {
      complete(row);
      return;
    }
    const agent = terminal ? 'shell' : controls.agent;
    const host = where ? where.host : row ? row.host : browseHost;
    const placement: Placement = { host, desktopId };
    const refused = refusal(host, agent);
    if (refused) {
      setError(refused);
      return;
    }
    const yolo = agent !== 'shell' && controls.yoloMode && controls.yoloSupported;
    const chief = agent !== 'shell' && controls.chiefOfStaff && controls.chiefToggleEligible;
    const request = generation.current;
    const current = { background: false };
    run.current = current;
    setBusy(true);
    setStarting({ host, at: Date.now() });
    setError(null);
    try {
      let path = row?.path;
      let repo = row?.repoRoot;
      let action = destination;
      if (!where && !row && browsing) {
        const typed = normalizePickerPath(browsing.path, listings.home(browseHost));
        const reply = await onInspectPath?.(typed, browseHost);
        if (request !== generation.current && !current.background) return;
        if (!reply?.inspection?.exists || !reply.inspection.is_directory)
          throw new Error(`Directory not found: ${typed}`);
        path = reply.inspection.resolved_path;
        repo = reply.inspection.repo_root === path ? path : undefined;
      }
      if (where) repo = where.repo;
      if (!action && repo)
        action =
          agent !== 'shell' && remember(repo, host) === 'new_worktree'
            ? { kind: 'new', from: 'default', branch: '' }
            : { kind: 'main', branch: '' };
      let destinationMemory: 'main_repo' | 'new_worktree' | undefined;
      if (action?.kind === 'new' && repo) {
        const generatedKey = onMachine(repo, host);
        if (!generated.current.has(generatedKey))
          generated.current.set(generatedKey, generateWorktreeName());
        const branchName = where ? name : generated.current.get(generatedKey)!;
        let startingFrom = action.branch;
        if (!where) {
          const reply = await onGetRepoInfo?.(repo, host);
          if (!reply?.success || !reply.info)
            throw new Error(reply?.error || 'Failed to read repository');
          startingFrom = reply.info.default_branch;
        }
        const key = `${generatedKey}:${branchName}:${startingFrom}`;
        const existing = created.current.get(key);
        if (existing) await onSelect(existing, agent, yolo, chief, placement);
        else
          await onCreateWorktreeSession(repo, branchName, startingFrom, agent, yolo, chief, {
            ...placement,
            onCreated: (path) => created.current.set(key, path),
          });
        destinationMemory = 'new_worktree';
      } else {
        const target =
          action?.kind === 'existing' ? action.path : action?.kind === 'main' ? repo : path;
        if (!target) return;
        await onSelect(target, agent, yolo, chief, placement);
        if (action?.kind === 'main') destinationMemory = 'main_repo';
      }
      const memoryKey = repo && repoDestinationSettingKey(repo, host);
      if (
        memoryKey &&
        destinationMemory &&
        agent !== 'shell' &&
        settings[memoryKey] !== destinationMemory
      )
        setSetting(memoryKey, destinationMemory);
      if (!current.background) onClose();
    } catch (reason) {
      if (!current.background) fail(reason);
      else
        onError?.(
          `Starting on ${machineName(host)} failed: ${reason instanceof Error ? reason.message : String(reason)}`,
        );
    } finally {
      if (!current.background) {
        run.current = null;
        setBusy(false);
        setStarting(null);
      }
    }
  }

  function complete(row: Row) {
    const machine = machineOf(row.host);
    const path =
      row.kind === 'machine' || row.kind === 'elsewhere'
        ? row.path
        : display(row.path, row.host);
    changeQuery(addressOf(machine, `${path.replace(/\/+$/, '')}/`));
  }

  async function tab() {
    if (where || busy) return;
    const row = rows[highlight];
    if (controls.agent !== 'shell' && row?.repoRoot) {
      await openWhere(row.repoRoot, row.host);
      return;
    }
    if (controls.agent !== 'shell' && !row && browsing) {
      const path = normalizePickerPath(browsing.path, listings.home(browseHost));
      const known = listings.known(browseHost, path);
      if (known === true) {
        await openWhere(path, browseHost);
        return;
      }
      if (known === undefined) {
        const request = generation.current;
        try {
          const reply = await onInspectPath?.(path, browseHost);
          if (request !== generation.current) return;
          if (
            reply?.inspection?.repo_root === reply?.inspection?.resolved_path &&
            reply?.inspection?.repo_root
          ) {
            await openWhere(reply.inspection.repo_root, browseHost);
            return;
          }
        } catch (reason) {
          fail(reason);
          return;
        }
      }
    }
    const target = row ?? rows.find((candidate) => candidate.kind === 'folder');
    if (target) complete(target);
  }

  async function deleteWorktree() {
    if (!deletion || !onDeleteWorktree || busy) return;
    setBusy(true);
    setError(null);
    try {
      const reply = await onDeleteWorktree(
        deletion.path,
        deletion.force ? { force: true } : undefined,
        where?.host,
      );
      if (!reply.success) throw new Error(reply.error || 'Failed to delete worktree');
      const remaining = whereRows.length - 1;
      setWhere((value) =>
        value?.info
          ? {
              ...value,
              info: {
                ...value.info,
                worktrees: value.info.worktrees.filter((item) => item.path !== deletion.path),
              },
            }
          : value,
      );
      setHighlight(Math.min(highlight, remaining - 1));
      setDeletion(null);
    } catch (reason) {
      fail(reason);
      if (!deletion.force && (reason as { forceable?: boolean }).forceable)
        setDeletion({ ...deletion, force: true });
      else setDeletion(null);
    } finally {
      setBusy(false);
    }
  }

  function keyDown(event: React.KeyboardEvent) {
    if (busy) {
      if (['Enter', 'Tab', 'Escape'].includes(event.key)) event.preventDefault();
      if (event.key === 'Escape' && stalled) {
        event.stopPropagation();
        background();
      }
      return;
    }
    if (deletion) {
      event.preventDefault();
      event.stopPropagation();
      if (event.key.toLowerCase() === 'y') void deleteWorktree();
      else if (event.key.toLowerCase() === 'n' || event.key === 'Escape') {
        setDeletion(null);
        setError(null);
      }
      return;
    }
    if (controls.handleControlKey(event)) return;
    const list = where ? whereRows : rows;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (list.length)
        setHighlight(
          event.key === 'ArrowDown'
            ? (highlight + 1) % list.length
            : highlight <= 0
              ? list.length - 1
              : highlight - 1,
        );
    } else if (event.key === 'Enter') {
      event.preventDefault();
      event.stopPropagation();
      if (where ? whereRows[highlight] : rows[highlight] || pathMode)
        void launch(
          rows[highlight],
          isAccelKeyPressed(event),
          where ? whereRows[highlight] : undefined,
        );
    } else if (event.key === 'Tab') {
      event.preventDefault();
      void tab();
    } else if (
      event.key === 'Escape' ||
      (where && event.key === 'Backspace' && !branch && !isAccelKeyPressed(event))
    ) {
      event.preventDefault();
      event.stopPropagation();
      stepBack();
    } else if (where && isAccelKeyPressed(event) && event.key === 'Backspace') {
      event.preventDefault();
      setError(null);
      const row = whereRows[highlight];
      if (row?.kind === 'existing')
        setDeletion({ path: row.path, branch: row.branch, force: false });
    }
  }

  if (!isOpen) return null;
  const selected = rows[highlight];
  const ghostRow = pathMode
    ? selected?.kind === 'folder'
      ? selected
      : highlight < 0
        ? rows.find((row) => row.kind === 'folder')
        : undefined
    : selected?.kind === 'machine'
      ? selected
      : undefined;
  const ghostFull = ghostRow
    ? addressOf(
        machineOf(ghostRow.host),
        `${(ghostRow.kind === 'machine' ? ghostRow.path : display(ghostRow.path, ghostRow.host)).replace(/\/+$/, '')}/`,
      )
    : '';
  const ghost = ghostFull.toLowerCase().startsWith(query.toLowerCase())
    ? query + ghostFull.slice(query.length)
    : '';
  const typedRepo =
    highlight < 0 &&
    browsing &&
    listings.known(
      browseHost,
      normalizePickerPath(browsing.path, listings.home(browseHost)),
    ) === true;
  const emptyText = !pathMode
    ? 'No recent project matches. Type ~/ to browse folders.'
    : current?.status === 'listed' || !browseReachable
      ? 'No folders match. Enter uses the path as typed.'
      : '';
  const remote = browsing?.machine.host;
  const elapsedSince = (started: number) => Math.max(0, Date.now() - started);
  const statusLine =
    !where && address.mode === 'ambiguous'
      ? `${address.prefix.slice(0, -1)} names ${address.machines.length} machines · pick one below`
      : !where && browsing && browseDir && remote
      ? !browseReachable
        ? current?.status === 'listed'
          ? `${browsing.machine.name} unreachable · folders as of ${listedAge(current.at)}`
          : `${browsing.machine.name} unreachable · no folders cached for ${browseDir.dir}`
        : current?.status === 'pending'
          ? `asking ${browsing.machine.name} for ${browseDir.dir}/ · ${elapsedSince(current.started)} ms`
          : current?.status === 'missing'
            ? `${browseDir.dir} is not on ${browsing.machine.name}`
            : current?.status === 'failed'
              ? `${browsing.machine.name} could not list ${browseDir.dir}: ${current.error}`
              : ''
      : '';
  const frame = (
    <div
      ref={root}
      className={`location-picker new-session-launcher${inline ? ' location-picker--inline' : ''}`}
      data-testid={inline ? 'location-picker-inline' : 'location-picker'}
      data-launcher="one-screen"
      onClick={(event) => event.stopPropagation()}
      onKeyDown={keyDown}
      aria-busy={busy}
    >
      <div className="launcher-controls">
        <SessionControls controls={controls} compact missing={missing} targetIsOutpost={Boolean(targetHost)} />
      </div>
      <div className="picker-header">
        <div className="picker-title" data-testid="location-picker-title">
          {controls.agent === 'shell' ? 'New terminal' : title}
        </div>
        <PathInput
          value={where ? branch : query}
          onChange={(value) => {
            if (busy || deletion) return;
            if (where) {
              setBranch(value);
              setError(null);
            } else changeQuery(value);
          }}
          leading={
            where && <span className="launcher-repo-chip">{pathBasename(where.repo)} ›</span>
          }
          ghostText={where ? '' : ghost}
          accent={where ? 0 : address.prefix.length}
          onKeyDown={(event) => {
            event.stopPropagation();
            keyDown(event);
          }}
          onSelect={() => {}}
          onSubmit={() => {}}
          onTabComplete={() => {}}
          placeholder={
            where
              ? 'name a new worktree, or pick below'
              : outposts.length
                ? 'Project, path, or machine:path'
                : 'Project or path'
          }
          autoFocus={active}
        />
        {!where && pathMode && currentDir && (
          <span
            className="picker-breadcrumb-path"
            data-testid="location-picker-breadcrumb-path"
          >
            {currentDir}
          </span>
        )}
        {statusLine && (
          <div
            className="launcher-status"
            role="status"
            data-testid="launcher-status"
            data-bad={
              address.mode === 'ambiguous' ||
              !browseReachable ||
              current?.status === 'missing' ||
              current?.status === 'failed'
                ? true
                : undefined
            }
          >
            {statusLine}
          </div>
        )}
      </div>
      {where ? (
        where.info ? (
          <WhereList
            rows={whereRows}
            highlight={highlight}
            repo={where.repo}
            displayPath={display}
            name={name}
            typed={Boolean(branch.trim())}
            remembered={memory}
            deletion={deletion}
            onPick={(index) => {
              if (busy || deletion) return;
              setHighlight(index);
              void launch(undefined, false, whereRows[index]);
            }}
          />
        ) : (
          <div className="picker-empty" role="status">
            {!error && `Reading ${pathBasename(where.repo)}…`}
          </div>
        )
      ) : (
        <div className="picker-results" role="listbox" aria-label="Locations">
          {rows.map((row, index) => (
            <Fragment key={`${row.kind}:${row.host ?? ''}:${row.path}`}>
              {(index === 0 || rows[index - 1].kind !== row.kind) && (
                <div className="picker-section-title">
                  {row.kind === 'recent'
                    ? 'Recent'
                    : row.kind === 'machine'
                      ? 'Browse a machine'
                      : row.kind === 'elsewhere'
                        ? 'Found elsewhere'
                        : `Folders in ${currentDir.replace(/\/$/, '')}/${outposts.length ? ` on ${machineName(browseHost)}` : ''}`}
                </div>
              )}
              <div
                className={`picker-item${highlight === index ? ' selected' : ''}`}
                role="option"
                aria-selected={highlight === index}
                data-testid={`location-picker-item-${index}`}
                data-index={index}
                data-kind={row.kind === 'folder' ? 'directory' : row.kind}
                data-path={row.path}
                data-host={row.host}
                data-reach={
                  row.host && row.kind !== 'machine'
                    ? (outpostOf(row.host)?.reach ?? 'unreachable')
                    : undefined
                }
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  setHighlight(index);
                  void launch(row);
                }}
              >
                <LauncherIcon
                  kind={
                    row.kind === 'machine' || row.kind === 'elsewhere'
                      ? row.kind
                      : row.kind === 'recent'
                        ? 'recent'
                        : row.repoRoot
                          ? 'repo'
                          : 'folder'
                  }
                />
                <div className="picker-info">
                  <div className="picker-name">
                    {row.name}
                    {row.kind === 'folder' && '/'}
                  </div>
                  {row.kind === 'recent' && (
                    <div className="picker-path">{display(row.path, row.host)}</div>
                  )}
                  {row.kind === 'machine' && (
                    <div className="picker-path">
                      {outpostOf(row.host!)?.state} · Tab browses {addressOf(machineOf(row.host), '~/')}
                    </div>
                  )}
                  {row.kind === 'elsewhere' && (
                    <div className="picker-path">
                      Enter browses {addressOf(machineOf(row.host), `${row.path}/`)}
                    </div>
                  )}
                </div>
                {row.repoRoot &&
                  (controls.agent !== 'shell' &&
                  (row.kind === 'recent' || highlight === index) ? (
                    <span
                      className={`launcher-badge launcher-destination${remember(row.repoRoot, row.host) === 'new_worktree' ? ' launcher-worktree' : ''}`}
                    >
                      {remember(row.repoRoot, row.host) === 'new_worktree'
                        ? '⎇ new worktree'
                        : 'main checkout'}
                    </span>
                  ) : (
                    row.kind === 'folder' && <span className="launcher-badge">repo</span>
                  ))}
                {(row.kind === 'recent' || row.kind === 'elsewhere') && outposts.length > 0 && (
                  <span
                    className="launcher-badge launcher-machine"
                    data-testid="launcher-machine"
                    data-state={row.host ? (outpostOf(row.host)?.state ?? 'lost') : 'local'}
                  >
                    {machineName(row.host)}
                  </span>
                )}
                {row.lastSeen && (
                  <span className="launcher-age">{locationAge(row.lastSeen)}</span>
                )}
              </div>
            </Fragment>
          ))}
          {!rows.length && emptyText && (
            <div className="picker-empty" data-testid="location-picker-empty">
              {emptyText}
            </div>
          )}
        </div>
      )}
      {error && (
        <div className="launcher-error" role="alert">
          {error.message}
          {error.reason && <div className="launcher-error-reason">{error.reason}</div>}
        </div>
      )}
      {busy && (
        <div className="launcher-progress" role="status" data-stalled={stalled || undefined}>
          {starting?.host ? `Starting on ${machineName(starting.host)}…` : 'Starting…'}
          {stalled && ` no answer for ${stalledSeconds}s`}
        </div>
      )}
      <div className="picker-footer">
        {stalled ? (
          <span>
            <kbd>Esc</kbd> keep waiting in the background
          </span>
        ) : deletion ? (
          <>
            <span>
              <kbd>y</kbd> delete
            </span>
            <span>
              <kbd>n</kbd> or <kbd>Esc</kbd> keep it
            </span>
          </>
        ) : (
          <>
            {where && <span>type to name it</span>}
            <span>
              <kbd>↑↓</kbd> {where ? 'choose' : 'move'}
            </span>
            <span>
              <kbd>Enter</kbd>{' '}
              {where ? 'start' : controls.agent === 'shell' ? 'open terminal' : 'start'}
            </span>
            {!where && (
              <span>
                <kbd>Tab</kbd>{' '}
                {controls.agent !== 'shell' && (selected?.repoRoot || typedRepo)
                  ? 'where…'
                  : 'complete'}
              </span>
            )}
            {(where || controls.agent !== 'shell') && (
              <span>
                <kbd>{keyCombo('accel', 'Enter')}</kbd> terminal here
              </span>
            )}
            {!where && outposts.length > 0 && (
              <span>
                <kbd>name:</kbd> machine
              </span>
            )}
            {where && whereRows[highlight]?.kind === 'existing' && (
              <span>
                <kbd>{keyCombo('accel', '⌫')}</kbd> delete
              </span>
            )}
            <span className="launcher-footer-back">
              <kbd>Esc</kbd> {where ? 'back' : 'close'}
            </span>
          </>
        )}
      </div>
    </div>
  );
  return inline ? (
    frame
  ) : (
    <div
      className="location-picker-overlay"
      data-testid="location-picker-overlay"
      onClick={() => {
        if (!busy) onClose();
      }}
    >
      {frame}
    </div>
  );
}
