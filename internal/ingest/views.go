package ingest

const buildColumns = `build_version, build_commit, build_channel, build_time, build_fingerprint,
	build_protocol, build_os, build_arch, build_dirty, app_version, app_channel`

func ofStream(stream string) string {
	return "(stream = '" + stream + "' OR startsWith(stream, '" + stream + "_m'))"
}

const outpost = `if(extract(stream, '_m([0-9a-f]+)$') = '', '', concat('h:', extract(stream, '_m([0-9a-f]+)$'))) AS outpost`

type view struct{ name, query string }

var views = []view{
	{"runs", `SELECT
		install_id, ` + outpost + `, subject AS session, at,
		JSONExtractString(props, 'started_by') AS started_by,
		JSONExtractInt(props, 'duration_ms') AS duration_ms,
		JSONExtractString(props, 'ended') AS ended,
		JSONExtract(props, 'opened_turn', 'Bool') AS opened_turn,
		JSONExtract(props, 'quiet_stop', 'Bool') AS quiet_stop,
		JSONExtract(props, 'turn_requested', 'Bool') AS turn_requested,
		JSONExtractString(props, 'turn_after_stop') AS turn_after_stop,
		JSONExtract(props, 'user_input_ms', 'Nullable(Int64)') AS user_input_ms,
		JSONExtractInt(props, 'approvals') AS approvals,
		JSONExtractString(props, 'usage') AS usage,
		JSONExtract(props, 'tokens', 'input', 'Nullable(Int64)') AS tokens_input,
		JSONExtract(props, 'tokens', 'output', 'Nullable(Int64)') AS tokens_output,
		JSONExtract(props, 'tokens', 'cache_read', 'Nullable(Int64)') AS tokens_cache_read,
		JSONExtract(props, 'tokens', 'cache_write', 'Nullable(Int64)') AS tokens_cache_write,
		JSONExtract(props, 'usd', 'Nullable(Float64)') AS usd,
		JSONExtractString(props, 'session', 'harness') AS harness,
		JSONExtractString(props, 'session', 'model') AS model,
		JSONExtractString(props, 'session', 'model_requested') AS model_requested,
		JSONExtractString(props, 'session', 'launch') AS launch,
		` + buildColumns + `
	FROM events WHERE ` + ofStream("run") + ` AND name = 'run'`},

	{"quiet_stops", `SELECT
		q.install_id AS install_id, q.outpost AS outpost, q.session AS session, q.at AS at,
		q.verdict AS verdict, q.failure AS failure,
		r.turn_after_stop AS turn_after_stop, r.turn_requested AS turn_requested,
		r.started_by AS started_by, r.harness AS harness, r.model AS model, r.launch AS launch,
		` + buildColumns + `
	FROM (
		SELECT install_id, stream, ` + outpost + `, subject AS session, at,
			JSONExtractString(props, 'verdict') AS verdict,
			JSONExtractString(props, 'failure') AS failure,
			` + buildColumns + `
		FROM events WHERE ` + ofStream("run") + ` AND name = 'quiet_stop'
	) AS q
	LEFT JOIN (
		SELECT install_id, stream, subject AS session, at,
			JSONExtractString(props, 'turn_after_stop') AS turn_after_stop,
			JSONExtract(props, 'turn_requested', 'Bool') AS turn_requested,
			JSONExtractString(props, 'started_by') AS started_by,
			JSONExtractString(props, 'session', 'harness') AS harness,
			JSONExtractString(props, 'session', 'model') AS model,
			JSONExtractString(props, 'session', 'launch') AS launch
		FROM events WHERE ` + ofStream("run") + ` AND name = 'run'
	) AS r ON q.install_id = r.install_id AND q.stream = r.stream AND q.session = r.session AND q.at = r.at`},

	{"cli_commands", `SELECT
		install_id, ` + outpost + `, subject AS session, at,
		name AS command,
		JSONExtractString(props, 'caller') AS caller,
		JSONExtractString(props, 'harness') AS harness,
		JSONExtract(props, 'ok', 'Bool') AS ok,
		JSONExtractInt(props, 'exit_code') AS exit_code,
		JSONExtractString(props, 'error_kind') AS error_kind,
		JSONExtractString(props, 'refusal') AS refusal,
		` + buildColumns + `
	FROM events WHERE ` + ofStream("cli") + ` AND at >= '2026-11-02'`},

	{"process_samples", `SELECT
		s.install_id AS install_id, s.outpost AS outpost, s.daemon_run AS daemon_run, s.at AS at,
		dateDiff('second', d.started_at, s.at) AS daemon_uptime_s,
		JSONExtractString(p, 'role') AS role,
		JSONExtractString(p, 'shell') AS shell,
		JSONExtractInt(p, 'count') AS count,
		JSONExtractUInt(p, 'resident_bytes') AS resident_bytes,
		JSONExtract(p, 'footprint_bytes', 'Nullable(UInt64)') AS footprint_bytes,
		JSONExtract(p, 'split', 'webkit_malloc_bytes', 'Nullable(UInt64)') AS webkit_malloc_bytes,
		JSONExtract(p, 'split', 'owned_graphics_bytes', 'Nullable(UInt64)') AS owned_graphics_bytes,
		JSONExtract(p, 'split', 'gigacage_bytes', 'Nullable(UInt64)') AS gigacage_bytes,
		JSONExtractInt(s.props, 'workload', 'sessions') AS sessions,
		JSONExtractInt(s.props, 'workload', 'agents') AS agents,
		JSONExtractInt(s.props, 'workload', 'terminals') AS terminals,
		JSONExtractInt(s.props, 'workload', 'panes_drawn') AS panes_drawn,
		JSONExtractInt(s.props, 'workload', 'desktops') AS desktops,
		JSONExtractInt(s.props, 'workload', 'seeds') AS seeds,
		` + buildColumns + `
	FROM (
		SELECT install_id, stream, ` + outpost + `, subject AS daemon_run, at, props, ` + buildColumns + `
		FROM events WHERE ` + ofStream("sample") + ` AND name = 'process'
	) AS s
	ARRAY JOIN JSONExtractArrayRaw(s.props, 'processes') AS p
	LEFT JOIN (
		SELECT install_id, stream, subject AS daemon_run, toNullable(min(at)) AS started_at
		FROM events WHERE ` + ofStream("sample") + ` AND name = 'daemon_start'
		GROUP BY install_id, stream, subject
	) AS d ON s.install_id = d.install_id AND s.stream = d.stream AND s.daemon_run = d.daemon_run`},

	{"daemon_starts", `SELECT
		install_id, ` + outpost + `, subject AS daemon_run, at,
		JSONExtractString(props, 'previous_end') AS previous_end,
		JSONExtractString(props, 'stop_reason') AS stop_reason,
		JSONExtractString(props, 'previous_run') AS previous_run,
		` + buildColumns + `
	FROM events WHERE ` + ofStream("sample") + ` AND name = 'daemon_start'`},

	{"app_actions", `SELECT
		install_id, at,
		name AS action,
		JSONExtractString(props, 'id') AS id,
		JSONExtractString(props, 'section') AS section,
		JSONExtractString(props, 'reason') AS reason,
		JSONExtractString(props, 'to') AS to,
		JSONExtract(props, 'wait_ms', 'Nullable(Int64)') AS wait_ms,
		JSONExtractString(props, 'kind') AS kind,
		` + buildColumns + `
	FROM events WHERE stream = 'app'`},
}
