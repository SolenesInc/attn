package analytics

var BusFields = map[string]Fields{
	"garden.seed.artifact.changed":        {"caused_by_session_id": ID},
	"garden.seed.body.edited":             {"caused_by_session_id": ID},
	"garden.seed.edge.linked":             {"edge_kind": Safe, "caused_by_session_id": ID, "target_seed_id": ID},
	"garden.seed.edge.unlinked":           {"edge_kind": Safe, "target_seed_id": ID},
	"garden.seed.harvest_when.cleared":    {"caused_by_session_id": ID, "pull_request_id": ID},
	"garden.seed.harvest_when.configured": {"caused_by_session_id": ID, "pull_request_id": ID},
	"garden.seed.harvested":               {"caused_by_session_id": ID},
	"garden.seed.note.added":              {"caused_by_session_id": ID, "note_id": ID},
	"garden.seed.parked":                  {"caused_by_session_id": ID},
	"garden.seed.planted":                 {"caused_by_session_id": ID},
	"garden.seed.replanted":               {"caused_by_session_id": ID},
	"garden.seed.tended":                  {"caused_by_session_id": ID, "directly_notified_session_id": ID},
	"garden.seed.unblocked":               {"caused_by_session_id": ID, "blocker_seed_id": ID},
	"garden.seed.withered":                {"caused_by_session_id": ID},
	"garden.seed.work.ready":              {"caused_by_session_id": ID, "automation_run_id": ID},
	"git.operation.started":               {"kind": Safe, "status": Safe, "id": ID},
	"git.operation.finished":              {"kind": Safe, "status": Safe, "id": ID},
	"notebook.file.changed":               {"origin": Safe},
	"profile.arrangement.changed": {
		"from_desktop_id": ID, "from_leaf_id": ID, "to_desktop_id": ID, "to_leaf_id": ID,
	},
	"quick_capture.read":           {"profile_id": ID},
	"ratelimit.hit":                {"reset_at": Safe},
	"seed_authorization.typed":     {"profile_id": ID},
	"session.background.launch":    {"event": Safe, "kind": Safe, "session_id": ID, "profile_id": ID, "desktop_id": ID},
	"session.pull_request.changed": {"pr_id": ID},
	"session.closed": {
		"agent": Safe, "state": Safe,
		"usage.models.model": Safe, "usage.models.purpose": Safe, "usage.models.unpriced_reason": Safe,
		"id": ID, "profile_id": ID, "workspace_id": ID,
	},
	"session.pty.exited": {"signal": Safe},
	"session.unregistered": {
		"agent": Safe, "state": Safe,
		"automation.trigger_type": Safe,
		"pull_requests.state":     Safe, "pull_requests.ci_status": Safe, "pull_requests.review_status": Safe,
		"pull_requests.mergeable_state": Safe, "pull_requests.readiness_state": Safe, "pull_requests.readiness_reason": Safe,
		"pull_requests.watch_mode": Safe, "pull_requests.watch_health": Safe,
		"usage.models.model": Safe, "usage.models.purpose": Safe, "usage.models.unpriced_reason": Safe,
		"id": ID, "profile_id": ID, "workspace_id": ID, "parent_session_id": ID, "dispatcher_session_id": ID,
		"succeeds": ID, "seed_id": ID, "automation.run_id": ID,
	},
	"workspace.layout.changed": {
		"panes.kind": Safe, "panes.status": Safe,
		"workspace_id": ID, "active_pane_id": ID,
		"panes.pane_id": ID, "panes.runtime_id": ID, "panes.session_id": ID, "panes.workspace_id": ID,
	},
	"workspace.unregistered": {"status": Safe, "id": ID},
	"worktree.state.changed": {"origin": Safe, "merged_signal": Safe, "sweep_status": Safe},
	"worktree.swept":         {"action": Safe},
}

var AppFields = map[string]Fields{
	"shortcut":         {"id": Safe},
	"palette_command":  {"id": Safe},
	"settings_section": {"section": Safe},
	"queue_advance":    {"reason": Safe, "to": Safe},
	"react_error":      {"kind": Safe},
}

var CLIFields = Fields{"error_kind": Safe, "refusal": Safe, "caller": Safe, "harness": Safe}
