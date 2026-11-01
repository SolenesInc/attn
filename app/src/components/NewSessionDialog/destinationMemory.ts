export type RepoDestination = 'new_worktree' | 'main_repo';

export function repoDestinationSettingKey(repoRoot: string, host?: string): string {
  return `new_session_destination_${host || 'local'}_${repoRoot}`;
}

export function parseRepoDestination(value?: string): RepoDestination | undefined {
  const normalized = value?.trim();
  return normalized === 'new_worktree' || normalized === 'main_repo' ? normalized : undefined;
}
