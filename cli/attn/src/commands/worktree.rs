use super::support::{client, parse_or_exit};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, absolute, print_reply_field, print_stdout};
use crate::client::SendError;
use crate::paths::base;
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::time::Time;
use attn_protocol::{
    DeleteWorktreeMessage, Worktree, WorktreeKeepMessage, WorktreeKeepResult, WorktreeListMessage,
    WorktreeListResult, WorktreeRefreshMessage, WorktreeRefreshResult, WorktreeSweepLogMessage,
    WorktreeSweepLogResult,
};
use std::collections::{BTreeMap, HashMap};
use std::fmt::Write as _;

const USAGE: &str = r"usage: attn worktree list [--repo <path>] [--limit <n>] [--json]
       attn worktree keep <path>
       attn worktree unkeep <path>
       attn worktree delete <path> [--force]
       attn worktree log [--repo <path>] [--limit <n>] [--json]
       attn worktree refresh

  list      every worktree of every tracked repository, with why the sweep kept it
  keep      pin a worktree so the sweep never reclaims it
  unkeep    drop the pin; the next sweep decides again
  delete    remove a worktree now; --force removes a dirty one
  log       what the sweep removed, newest first
  refresh   ask the daemon to refresh worktree state now instead of on its tick
";

const DEFAULT_LIMIT: i64 = 20;

fn failed(run: &mut Run<'_>, error: &SendError) -> Stop {
    run.fail_send("error", error)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first().map(String::as_str) else {
        eprint!("{USAGE}");
        return Err(Stop(1));
    };
    let rest = &args[1..];
    if matches!(
        verb,
        "list" | "keep" | "unkeep" | "delete" | "log" | "refresh"
    ) {
        run.path = format!("worktree {verb}");
    }
    match verb {
        "list" => list(run, rest),
        "keep" => keep(run, rest, true),
        "unkeep" => keep(run, rest, false),
        "delete" => delete(run, rest),
        "log" => log(run, rest),
        "refresh" => refresh(run),
        "-h" | "--help" | "help" => {
            print_stdout(USAGE);
            Ok(())
        }
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!("unknown worktree command: {verb}\n\n{USAGE}");
            Err(Stop(1))
        }
    }
}

struct Query {
    repo: Option<String>,
    limit: Option<i64>,
    json: bool,
}

fn query_flags(run: &mut Run<'_>, name: &str, args: &[String]) -> Result<Query, Stop> {
    let set_name = format!("worktree {name}");
    let mut flags = FlagSet::new(
        &set_name,
        &[
            Flag {
                name: "repo",
                default: FlagDefault::String(""),
                usage: "only this repository",
            },
            Flag {
                name: "limit",
                default: FlagDefault::Int(DEFAULT_LIMIT),
                usage: "how many rows to show",
            },
            Flag {
                name: "json",
                default: FlagDefault::Bool(false),
                usage: "print the raw result",
            },
        ],
    );
    parse_or_exit(run, &mut flags, args)?;
    let repo = flags.string("repo");
    Ok(Query {
        repo: (!repo.trim().is_empty()).then(|| path_argument(repo)),
        limit: Some(flags.int("limit")).filter(|limit| *limit > 0),
        json: flags.bool("json"),
    })
}

fn path_argument(value: &str) -> String {
    let trimmed = value.trim();
    absolute(trimmed).unwrap_or_else(|_| trimmed.to_string())
}

fn omitted_line(shown: usize, omitted: i64) -> String {
    let total = i64::try_from(shown).unwrap_or(i64::MAX) + omitted;
    format!("\nshowing {shown}, {omitted} omitted, raise the page with --limit {total}\n")
}

fn list(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let query = query_flags(run, "list", args)?;
    let mut message = WorktreeListMessage::new();
    message.main_repo = query.repo;
    message.limit = query.limit;
    let reply = client(run)
        .call_field(
            &message,
            "worktree_list_result",
            "daemon returned no worktree list",
        )
        .map_err(|error| failed(run, &error))?;
    if query.json {
        return print_reply_field(&reply, &["worktree_list_result"])
            .map_err(|error| failed(run, &error));
    }
    let result: WorktreeListResult = reply
        .value("worktree_list_result")
        .map_err(|error| failed(run, &error))?;
    print_stdout(&render_list(&result));
    Ok(())
}

fn render_list(result: &WorktreeListResult) -> String {
    if result.worktrees.is_empty() {
        return "no worktrees tracked yet; run `attn worktree refresh` if a repository is missing\n"
            .to_string();
    }
    let integration: HashMap<&str, String> = result
        .repositories
        .iter()
        .filter_map(|repository| {
            let branch = repository
                .integration_branch
                .as_deref()
                .filter(|branch| !branch.is_empty())?;
            let source = repository.integration_source.as_deref().unwrap_or("");
            Some((
                repository.main_repo.as_str(),
                format!("{branch} ({source})"),
            ))
        })
        .collect();
    let mut by_repo: BTreeMap<&str, Vec<&Worktree>> = BTreeMap::new();
    for worktree in &result.worktrees {
        by_repo
            .entry(worktree.main_repo.as_str())
            .or_default()
            .push(worktree);
    }
    let mut out = String::new();
    for (index, (repo, worktrees)) in by_repo.iter().enumerate() {
        if index > 0 {
            out.push('\n');
        }
        out.push_str(repo);
        if let Some(branch) = integration.get(repo) {
            let _ = write!(out, "   merges into {branch}");
        }
        out.push('\n');
        let mut rows = vec![
            ["WORKTREE", "BRANCH", "STATE", "SWEEP", "WHY"]
                .map(String::from)
                .to_vec(),
        ];
        for worktree in worktrees {
            rows.push(vec![
                base(&worktree.path).to_string(),
                branch_label(worktree),
                state_label(worktree),
                sweep_label(worktree),
                worktree.sweep_reason.clone().unwrap_or_default(),
            ]);
        }
        out.push_str(&format_table(&rows));
    }
    if result.omitted > 0 {
        out.push_str(&omitted_line(result.worktrees.len(), result.omitted));
    }
    out
}

fn branch_label(worktree: &Worktree) -> String {
    if worktree.detached == Some(true) {
        let sha = worktree.head_sha.as_deref().unwrap_or("");
        return format!("detached {}", sha.get(..8).unwrap_or(sha));
    }
    if worktree.branch.is_empty() {
        return "-".to_string();
    }
    worktree.branch.clone()
}

fn state_label(worktree: &Worktree) -> String {
    let mut parts = Vec::new();
    if worktree.prunable == Some(true) {
        parts.push("stale".to_string());
    }
    if worktree.dirty == Some(true) {
        parts.push(format!("dirty {}", worktree.dirty_files.unwrap_or(0)));
    }
    if let Some(count) = worktree.stashes.filter(|count| *count > 0) {
        parts.push(format!("{count} stashed"));
    }
    if let Some(count) = worktree.unpushed.filter(|count| *count > 0) {
        parts.push(format!("{count} ahead"));
    }
    if let Some(signal) = worktree
        .merged_signal
        .as_deref()
        .filter(|signal| !signal.is_empty())
    {
        parts.push(format!("merged/{signal}"));
    }
    if worktree.observed_at.as_deref().unwrap_or("").is_empty() {
        parts.push("not refreshed".to_string());
    }
    if parts.is_empty() {
        return "clean".to_string();
    }
    parts.join(", ")
}

fn sweep_label(worktree: &Worktree) -> String {
    let status = worktree.sweep_status.as_deref().unwrap_or("");
    if status.is_empty() {
        return "-".to_string();
    }
    if status != "scheduled" {
        return status.to_string();
    }
    match Time::parse_rfc3339(worktree.sweep_at.as_deref().unwrap_or("")) {
        Ok(at) => format!("scheduled {}", at.format("2006-01-02")),
        Err(_) => status.to_string(),
    }
}

fn keep(run: &mut Run<'_>, args: &[String], keep: bool) -> Result<(), Stop> {
    let Some(path) = args.first().filter(|path| !path.trim().is_empty()) else {
        let verb = if keep { "keep" } else { "unkeep" };
        eprintln!("usage: attn worktree {verb} <path>");
        return Err(Stop(1));
    };
    let mut message = WorktreeKeepMessage::new();
    message.path = path_argument(path);
    message.keep = keep;
    let reply = client(run)
        .call_field(
            &message,
            "worktree_keep_result",
            "daemon returned no worktree",
        )
        .map_err(|error| failed(run, &error))?;
    let result: WorktreeKeepResult = reply
        .value("worktree_keep_result")
        .map_err(|error| failed(run, &error))?;
    let path = &result.worktree.path;
    print_stdout(&if keep {
        format!("pinned {path}; the sweep will never reclaim it\n")
    } else {
        format!("unpinned {path}; the next sweep decides again\n")
    });
    Ok(())
}

fn delete(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "worktree delete",
        &[Flag {
            name: "force",
            default: FlagDefault::Bool(false),
            usage: "delete even when the tree has uncommitted or untracked files",
        }],
    );
    parse_or_exit(run, &mut flags, args)?;
    let Some(first) = flags.args().first() else {
        eprintln!("usage: attn worktree delete <path> [--force]");
        return Err(Stop(1));
    };
    let path = path_argument(first);
    let mut message = DeleteWorktreeMessage::new();
    message.path.clone_from(&path);
    message.force = Some(flags.bool("force"));
    client(run)
        .send(&message)
        .map_err(|error| failed(run, &error))?;
    print_stdout(&format!("deleted {path}\n"));
    Ok(())
}

fn log(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let query = query_flags(run, "log", args)?;
    let mut message = WorktreeSweepLogMessage::new();
    message.main_repo = query.repo;
    message.limit = query.limit;
    let reply = client(run)
        .call_field(
            &message,
            "worktree_sweep_log_result",
            "daemon returned no sweep log",
        )
        .map_err(|error| failed(run, &error))?;
    if query.json {
        return print_reply_field(&reply, &["worktree_sweep_log_result"])
            .map_err(|error| failed(run, &error));
    }
    let result: WorktreeSweepLogResult = reply
        .value("worktree_sweep_log_result")
        .map_err(|error| failed(run, &error))?;
    if result.entries.is_empty() {
        print_stdout("the sweep has not removed anything yet\n");
        return Ok(());
    }
    let mut rows = vec![
        ["WHEN", "WORKTREE", "BRANCH", "ACTION", "WHY"]
            .map(String::from)
            .to_vec(),
    ];
    for entry in &result.entries {
        let when = Time::parse_rfc3339_nano(&entry.at)
            .map_or_else(|_| entry.at.clone(), |at| at.format("2006-01-02 15:04"));
        rows.push(vec![
            when,
            entry.path.clone(),
            entry.branch.clone().unwrap_or_default(),
            entry.action.clone(),
            entry.reason.clone().unwrap_or_default(),
        ]);
    }
    let mut out = format_table(&rows);
    if result.omitted > 0 {
        out.push_str(&omitted_line(result.entries.len(), result.omitted));
    }
    print_stdout(&out);
    Ok(())
}

fn refresh(run: &mut Run<'_>) -> Result<(), Stop> {
    let reply = client(run)
        .call_field(
            &WorktreeRefreshMessage::new(),
            "worktree_refresh_result",
            "daemon returned no refresh result",
        )
        .map_err(|error| failed(run, &error))?;
    let result: WorktreeRefreshResult = reply
        .value("worktree_refresh_result")
        .map_err(|error| failed(run, &error))?;
    if !result.queued {
        eprintln!("the daemon has no job queue running, so no refresh was queued");
        return Err(Stop(1));
    }
    print_stdout(
        "refreshing worktree state in the background; `attn worktree list` shows the result\n",
    );
    Ok(())
}

pub fn format_table(rows: &[Vec<String>]) -> String {
    let Some(header) = rows.first() else {
        return String::new();
    };
    let mut widths = vec![0; header.len()];
    for row in rows {
        for (width, cell) in widths.iter_mut().zip(row) {
            *width = (*width).max(cell.len());
        }
    }
    let mut out = String::new();
    for row in rows {
        let mut line = String::new();
        for (index, cell) in row.iter().enumerate() {
            line.push_str(cell);
            if index + 1 < row.len() {
                line.push_str(&" ".repeat(widths[index] - cell.len() + 2));
            }
        }
        out.push_str(line.trim_end_matches(' '));
        out.push('\n');
    }
    out
}
