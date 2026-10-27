package analytics

var SafeValues = map[string]Allowlist{
	"garden.seed.edge.linked":   {"edge_kind": true},
	"garden.seed.edge.unlinked": {"edge_kind": true},
	"git.operation.started":     {"kind": true, "status": true},
	"git.operation.finished":    {"kind": true, "status": true},
	"notebook.file.changed":     {"origin": true},
	"ratelimit.hit":             {"reset_at": true},
	"session.background.launch": {"event": true, "kind": true},
	"session.closed": {
		"agent": true, "state": true,
		"usage.models.model": true, "usage.models.purpose": true, "usage.models.unpriced_reason": true,
	},
	"session.pty.exited": {"signal": true},
	"session.unregistered": {
		"agent": true, "state": true,
		"automation.trigger_type": true,
		"pull_requests.state":     true, "pull_requests.ci_status": true, "pull_requests.review_status": true,
		"pull_requests.mergeable_state": true, "pull_requests.readiness_state": true, "pull_requests.readiness_reason": true,
		"pull_requests.watch_mode": true, "pull_requests.watch_health": true,
		"usage.models.model": true, "usage.models.purpose": true, "usage.models.unpriced_reason": true,
	},
	"workspace.layout.changed": {"panes.kind": true, "panes.status": true},
	"workspace.unregistered":   {"status": true},
	"worktree.state.changed":   {"origin": true, "merged_signal": true, "sweep_status": true},
	"worktree.swept":           {"action": true},
}

var AppSafeValues = map[string]Allowlist{
	"shortcut":         {"id": true},
	"palette_command":  {"id": true},
	"settings_section": {"section": true},
	"queue_advance":    {"reason": true, "to": true},
	"react_error":      {"kind": true},
}

var CLISafeValues = Allowlist{"error_kind": true, "caller": true, "harness": true}
