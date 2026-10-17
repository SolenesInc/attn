export type RepoDestination = 'new_worktree' | 'main_repo';

export function repoDestinationSettingKey(repoRoot: string): string {
  return `new_session_destination_local_${repoRoot}`;
}

export function parseRepoDestination(value?: string): RepoDestination | undefined {
  const normalized = value?.trim();
  return normalized === 'new_worktree' || normalized === 'main_repo' ? normalized : undefined;
}
