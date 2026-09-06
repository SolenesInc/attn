import { createContext, useContext, type ReactNode } from 'react';

// The daemon's reason for not polling GitHub in this profile; null while it polls.
const GitHubPollingOffContext = createContext<string | null>(null);

export function GitHubPollingProvider({ offReason, children }: { offReason: string | null; children: ReactNode }) {
  return <GitHubPollingOffContext.Provider value={offReason}>{children}</GitHubPollingOffContext.Provider>;
}

export function useGitHubPollingOffReason(): string | null {
  return useContext(GitHubPollingOffContext);
}
