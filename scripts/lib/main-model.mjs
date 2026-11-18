export function mainModel(usage) {
  const agents = (usage?.models ?? []).filter((m) => m.purpose === 'agent');
  return agents.sort((a, b) => (b.cost_usd ?? 0) - (a.cost_usd ?? 0))[0]?.model ?? null;
}
