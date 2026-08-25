export function getRepoName(fullRepo: string): string {
  const parts = fullRepo.split('/');
  return parts[1] || fullRepo;
}
