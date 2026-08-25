import { create } from 'zustand';
import { DaemonSession, DaemonPR, RepoState, AuthorState, Seed, CrewMember, AppRegistryEntry } from '../hooks/useDaemonSocket';

interface DaemonStore {
  daemonSessions: DaemonSession[];
  setDaemonSessions: (sessions: DaemonSession[]) => void;


  // Newest first. `seedsTotal` exceeds seeds.length only when the garden outgrew one push.
  seeds: Seed[];
  seedsTotal: number;
  setSeeds: (seeds: Seed[], total: number) => void;

  crew: CrewMember[];
  setCrew: (crew: CrewMember[]) => void;

  prs: DaemonPR[];
  setPRs: (prs: DaemonPR[]) => void;

  apps: AppRegistryEntry[];
  setApps: (apps: AppRegistryEntry[]) => void;

  repoStates: RepoState[];
  setRepoStates: (repos: RepoState[]) => void;

  authorStates: AuthorState[];
  setAuthorStates: (authors: AuthorState[]) => void;

  isRepoMuted: (repo: string) => boolean;

  isAuthorMuted: (author: string) => boolean;

  isConnected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useDaemonStore = create<DaemonStore>((set, get) => ({
  daemonSessions: [],
  setDaemonSessions: (sessions) => set({ daemonSessions: sessions }),


  seeds: [],
  seedsTotal: 0,
  setSeeds: (seeds, total) => set({ seeds, seedsTotal: total }),

  crew: [],
  setCrew: (crew) => set({ crew }),

  prs: [],
  setPRs: (prs) => set({ prs }),

  apps: [],
  setApps: (apps) => set({ apps }),

  repoStates: [],
  setRepoStates: (repos) => set({ repoStates: repos }),

  authorStates: [],
  setAuthorStates: (authors) => set({ authorStates: authors }),

  isRepoMuted: (repo) => {
    const state = get().repoStates.find(r => r.repo === repo);
    return state?.muted ?? false;
  },

  isAuthorMuted: (author) => {
    const state = get().authorStates.find(a => a.author === author);
    return state?.muted ?? false;
  },

  isConnected: false,
  setConnected: (connected) => set({ isConnected: connected }),
}));
