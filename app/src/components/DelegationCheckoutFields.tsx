export type DelegationCheckoutDraft = {
  cwd: string;
  checkoutKind: 'none' | 'reuse' | 'new_worktree' | 'existing_branch_worktree';
  branch: string;
  from: string;
  path: string;
  agent: string;
  allowWorktreeReuse: boolean;
};

export function DelegationCheckoutFields({ value, onChange, agentHint = false }: {
  value: DelegationCheckoutDraft;
  onChange: (patch: Partial<DelegationCheckoutDraft>) => void;
  agentHint?: boolean;
}) {
  const hasCheckout = value.checkoutKind !== 'none';
  const newWorktree = value.checkoutKind === 'new_worktree';
  const worktree = newWorktree || value.checkoutKind === 'existing_branch_worktree';
  return <>
    <label>Working folder<input required value={value.cwd} onChange={(event) => onChange({ cwd: event.target.value })} /></label>
    <label>Git checkout<select value={value.checkoutKind} onChange={(event) => onChange({ checkoutKind: event.target.value as DelegationCheckoutDraft['checkoutKind'] })}>
      <option value="none">Not a Git folder</option>
      <option value="reuse">Reuse this checkout</option>
      <option value="new_worktree">Create a new branch worktree</option>
      <option value="existing_branch_worktree">Create a worktree for an existing branch</option>
    </select></label>
    {hasCheckout && <label>Branch<input required value={value.branch} onChange={(event) => onChange({ branch: event.target.value })} /></label>}
    {newWorktree && <label>Start from<input required value={value.from} onChange={(event) => onChange({ from: event.target.value })} placeholder="origin/main or a commit" /></label>}
    {worktree && <label>Worktree path<span>optional</span><input value={value.path} onChange={(event) => onChange({ path: event.target.value })} /></label>}
    {hasCheckout && <label><input type="checkbox" checked={value.allowWorktreeReuse} onChange={(event) => onChange({ allowWorktreeReuse: event.target.checked })} />Allow sharing an occupied checkout</label>}
    <label>Agent{agentHint && !value.agent ? <span>required without a source session</span> : null}<input value={value.agent} onChange={(event) => onChange({ agent: event.target.value })} placeholder="codex" /></label>
  </>;
}
