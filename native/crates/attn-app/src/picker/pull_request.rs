use attn_client::FETCH_PR_DETAILS_ID;
use attn_protocol::{
    CreateWorktreeFromBranchMessage, CreateWorktreeResultMessage, EnsureRepoMessage,
    FetchPRDetailsMessage, FetchPRDetailsResultMessage, PR,
};
use rune::Tx;

use super::repo::{Setup, Step};
use super::{Agents, Direction, PROJECTS, SAVED_AGENT, Spawn, TERMINAL, spawn};
use crate::State;

/// Where a pull request's agent starts: a worktree of the pull request's branch in the user's
/// clone of its repository, under the projects directory.
struct Checkout {
    pr: String,
    host: String,
    repo: String,
    local: String,
    agent: String,
    desktop: Option<String>,
    subject: String,
}

/// Starts the user's agent in a new worktree of `pr`'s branch, as the Tauri app's New worktree
/// session does: the daemon fetches the branch name when it is not known yet, clones or fetches
/// the repository, makes the worktree, then the agent starts in it on the current desktop. The
/// corner prompt says how far it got.
pub(crate) fn open_in_worktree(state: &mut State, pr: &PR, tx: &mut Tx) {
    let failed = |setup: &Setup| setup.failed.is_some();
    if state
        .window
        .launching
        .worktree
        .as_ref()
        .is_some_and(|s| !failed(s))
    {
        state
            .window
            .notify("A worktree session is already being created.".into(), tx);
        return;
    }
    let settings = &state.facts.settings;
    let preferred = settings
        .get(SAVED_AGENT)
        .map(String::as_str)
        .filter(|a| *a != TERMINAL)
        .unwrap_or("claude");
    let agent = Agents::from_settings(settings).resolve(preferred);
    if agent == TERMINAL {
        state
            .window
            .notify("No supported agent CLI found in PATH.".into(), tx);
        return;
    }
    let Some(projects) = settings
        .get(PROJECTS)
        .map(|dir| dir.trim_end_matches('/'))
        .filter(|dir| !dir.is_empty())
    else {
        state.window.notify(
            "Configure your Projects Directory in Settings first: it tells attn where to find your repositories."
                .into(),
            tx,
        );
        return;
    };
    let name = pr.repo.rsplit('/').next().unwrap_or(&pr.repo);
    let checkout = Checkout {
        pr: pr.id.clone(),
        host: pr.host.clone(),
        repo: pr.repo.clone(),
        local: format!("{projects}/{name}"),
        agent,
        desktop: state.facts.current_desktop().map(|d| d.id.clone()),
        subject: format!("{name}#{}", pr.number),
    };
    match pr.head_branch.clone().filter(|b| !b.is_empty()) {
        Some(branch) => sync(state, checkout, branch, tx),
        None => fetch_branch(state, checkout, tx),
    }
}

fn fetch_branch(state: &mut State, checkout: Checkout, tx: &mut Tx) {
    state.window.launching.worktree = Some(Setup::new(&checkout.subject, Step::FetchingDetails));
    let message = FetchPRDetailsMessage {
        id: checkout.pr.clone(),
        ..FetchPRDetailsMessage::new()
    };
    state.ask_as(
        FETCH_PR_DETAILS_ID.to_owned(),
        &message,
        move |state, answer, tx| {
            let fetched = match answer.outcome() {
                Ok(reply) => reply.parse::<FetchPRDetailsResultMessage>(),
                Err(reason) => return fail(state, format!("Failed to fetch PR details. {reason}")),
            };
            let branch = fetched
                .and_then(|r| r.prs)
                .into_iter()
                .flatten()
                .find(|p| p.id == checkout.pr)
                .and_then(|p| p.head_branch)
                .filter(|b| !b.is_empty());
            match branch {
                Some(branch) => sync(state, checkout, branch, tx),
                None => fail(
                    state,
                    "PR branch information not available. Refresh PRs to fetch branch details."
                        .into(),
                ),
            }
        },
        tx,
    );
}

fn sync(state: &mut State, checkout: Checkout, branch: String, tx: &mut Tx) {
    state.window.launching.worktree = Some(Setup::new(
        &checkout.subject,
        Step::SyncingRepo(checkout.local.clone()),
    ));
    let message = EnsureRepoMessage {
        target_path: checkout.local.clone(),
        clone_url: format!("https://{}/{}.git", checkout.host, checkout.repo),
        ..EnsureRepoMessage::new()
    };
    state.ask_as(
        checkout.local.clone(),
        &message,
        move |state, answer, tx| match answer.outcome() {
            Ok(_) => add_worktree(state, checkout, branch, tx),
            Err(reason) => fail(state, explain(&checkout.repo, reason)),
        },
        tx,
    );
}

fn add_worktree(state: &mut State, checkout: Checkout, branch: String, tx: &mut Tx) {
    if let Some(setup) = &mut state.window.launching.worktree {
        setup.step = Step::CreatingWorktree(checkout.local.clone());
    }
    let (main_repo, repo) = (checkout.local.clone(), checkout.repo.clone());
    state.ask(
        |id| CreateWorktreeFromBranchMessage {
            main_repo,
            branch: format!("origin/{branch}"),
            request_id: Some(id),
            ..CreateWorktreeFromBranchMessage::new()
        },
        move |state, answer, tx| {
            let path = match answer.parse::<CreateWorktreeResultMessage>() {
                Some(CreateWorktreeResultMessage {
                    success: true,
                    path: Some(path),
                    ..
                }) => path,
                other => {
                    let error = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to create worktree".into());
                    return fail(state, explain(&repo, error));
                }
            };
            state.window.launching.worktree = None;
            spawn(
                state,
                Spawn {
                    path,
                    agent: checkout.agent,
                    yolo: false,
                    desktop: checkout.desktop,
                    anchor: None,
                    direction: Direction::Vertical,
                    label: Some(checkout.subject),
                    host: None,
                },
                tx,
            );
        },
        tx,
    );
}

/// What the user can do about a git step that failed, as the Tauri app words it.
fn explain(repo: &str, error: String) -> String {
    if error.contains("clone failed") {
        format!(
            "Failed to clone repository {repo}. Check your network connection and GitHub access. {error}"
        )
    } else if error.contains("already exists") {
        format!("A worktree for this branch may already exist. {error}")
    } else {
        format!("Failed to open PR: {error}")
    }
}

fn fail(state: &mut State, error: String) {
    if let Some(setup) = &mut state.window.launching.worktree {
        setup.fail(error);
    }
}
