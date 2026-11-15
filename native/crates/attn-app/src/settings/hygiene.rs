//! Settings → Attention queue: auto-settle and its two windows, which agents join the queue,
//! and the repositories and authors muted out of it.

use std::collections::BTreeSet;

use attn_protocol::{MuteAuthorMessage, MuteRepoMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, col, row, text};

use super::pages::{
    NUMBER_FIELD, Tone, block, button, pill, row_card, setting_field, setting_switch,
};
use crate::action::Action;
use crate::{State, setting};

pub(super) const AUTO_SETTLE_ARM: &str = "auto_settle_arm_seconds";
pub(super) const AUTO_SETTLE_COUNTDOWN: &str = "auto_settle_countdown_seconds";
const AUTO_SETTLE: &str = "auto_settle_enabled";
const QUIET_NOTIFICATIONS: &str = "queue_quiet_notifications";
const HIDDEN_SESSIONS: &str = "queue_show_hidden_sessions";
const CREW: &str = "queue_crew_enabled";

/// Unmutes the live connection took, hidden until the daemon reports them unmuted or gone, the
/// connection drops, or the dialog opens again: the daemon's mute commands toggle, so a second
/// press on a row still shown would mute it again.
#[derive(Default)]
pub(super) struct Hygiene {
    unmuting_repos: BTreeSet<String>,
    unmuting_authors: BTreeSet<String>,
}

pub(super) fn forget(state: &mut State) {
    state.window.settings.hygiene = Hygiene::default();
}

pub(super) fn poll(state: &mut State) {
    let prs = &state.facts.pull_requests;
    let hygiene = &mut state.window.settings.hygiene;
    hygiene
        .unmuting_repos
        .retain(|name| prs.repos.iter().any(|r| &r.repo == name && r.muted));
    hygiene
        .unmuting_authors
        .retain(|name| prs.authors.iter().any(|a| &a.author == name && a.muted));
}

fn muted_repos(state: &State) -> Vec<String> {
    let unmuting = &state.window.settings.hygiene.unmuting_repos;
    state
        .facts
        .pull_requests
        .repos
        .iter()
        .filter(|r| r.muted && !unmuting.contains(&r.repo))
        .map(|r| r.repo.clone())
        .collect()
}

fn muted_authors(state: &State) -> Vec<String> {
    let unmuting = &state.window.settings.hygiene.unmuting_authors;
    state
        .facts
        .pull_requests
        .authors
        .iter()
        .filter(|a| a.muted && !unmuting.contains(&a.author))
        .map(|a| a.author.clone())
        .collect()
}

fn unmute_repo(state: &mut State, repo: &str) {
    if state.window.settings.hygiene.unmuting_repos.contains(repo) {
        return;
    }
    let sent = state.daemon.send(&MuteRepoMessage {
        repo: repo.to_owned(),
        ..MuteRepoMessage::new()
    });
    if sent.is_ok() {
        state
            .window
            .settings
            .hygiene
            .unmuting_repos
            .insert(repo.to_owned());
    }
}

fn unmute_author(state: &mut State, author: &str) {
    if state
        .window
        .settings
        .hygiene
        .unmuting_authors
        .contains(author)
    {
        return;
    }
    let sent = state.daemon.send(&MuteAuthorMessage {
        author: author.to_owned(),
        ..MuteAuthorMessage::new()
    });
    if sent.is_ok() {
        state
            .window
            .settings
            .hygiene
            .unmuting_authors
            .insert(author.to_owned());
    }
}

fn muted_list(
    kind: &str,
    title: &str,
    names: Vec<String>,
    empty: &str,
    t: &Tokens,
    unmute: fn(&mut State, &str),
) -> El {
    if names.is_empty() {
        return text(empty.to_owned())
            .key(format!("settings.muted.{kind}.empty"))
            .font(type_::BODY)
            .color(t.text_tertiary);
    }
    col()
        .key(format!("settings.muted.{kind}"))
        .role("list")
        .label(title.to_owned())
        .w_full()
        .gap(space::S1)
        .children(names.into_iter().map(|name| {
            let pressed = name.clone();
            row()
                .key(format!("settings.muted.{kind}.{name}"))
                .role("listitem")
                .label(name.clone())
                .w_full()
                .gap(space::S2)
                .items_center()
                .padding(space::S2)
                .radius(6.0)
                .fill(t.bg_panel)
                .child(
                    text(name.clone())
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .grow(1.0)
                        .min_w(0.0),
                )
                .child(button(
                    format!("settings.unmute.{kind}.{name}"),
                    "Unmute",
                    t,
                    move |state, _| unmute(state, &pressed),
                ))
        }))
}

fn shortcut(state: &State, action: &Action) -> String {
    state
        .keymap
        .binding(action)
        .map(|b| state.keymap.label(b))
        .unwrap_or_default()
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let settle = shortcut(state, &Action::SessionSettle);
    let keep = shortcut(state, &Action::SessionCancelCountdown);
    vec![
        block(
            "auto-settle",
            "Auto-settle",
            "Closes a turn for you once you have steered the agent and walked away, so the queue does not fill up with turns you have already dealt with. Off by default.",
            t,
            vec![
                row_card(
                    "Settle a turn once you have steered the agent",
                    &format!(
                        "When an agent you owe a turn goes back to work and stays there, its terminal tile runs a countdown and then settles the turn for you — the same thing {settle} does. Press {keep} to keep the turn instead. Anything that makes the agent want you again — a question, an approval, an error, a finished run — cancels it. Off by default."
                    ),
                    t,
                    setting_switch(
                        state,
                        AUTO_SETTLE,
                        "Settle a turn once you have steered the agent",
                        t,
                    ),
                ),
                row_card(
                    "Wait before counting down (seconds)",
                    "How long the agent must keep working before anything starts. Nothing is shown during this window.",
                    t,
                    setting_field(
                        state,
                        AUTO_SETTLE_ARM,
                        "Wait before counting down (seconds)",
                        "",
                        NUMBER_FIELD,
                        t,
                    ),
                ),
                row_card(
                    "Countdown before settling (seconds)",
                    &format!(
                        "How long the countdown runs on the tile — your window to press {keep} and keep the turn."
                    ),
                    t,
                    setting_field(
                        state,
                        AUTO_SETTLE_COUNTDOWN,
                        "Countdown before settling (seconds)",
                        "",
                        NUMBER_FIELD,
                        t,
                    ),
                ),
            ],
        ),
        block(
            "quiet-notifications",
            "Quiet notifications",
            "Keeps agents whose run attn started only to deliver a notification from sending you to them. On by default.",
            t,
            vec![row_card(
                "Quiet notifications in the queue",
                "When attn starts an agent's run to deliver a notification (a message, a seed update, a pull request update), the agent stays out of your queue when it stops, unless it needs an approval or opens its turn to ask for you. An agent that was waiting for you leaves the queue when a notification sets it working, without moving you.",
                t,
                setting_switch(
                    state,
                    QUIET_NOTIFICATIONS,
                    "Quiet notifications in the queue",
                    t,
                ),
            )],
        ),
        block(
            "hidden-sessions",
            "Hidden sessions",
            "Shared Codex sessions that keep running without an attn tile. On by default.",
            t,
            vec![row_card(
                "Show hidden sessions in the queue",
                "Switching sessions inside Codex can leave the previous session running without a tile. Include these hidden sessions in the queue; selecting one opens it in a terminal.",
                t,
                setting_switch(
                    state,
                    HIDDEN_SESSIONS,
                    "Show hidden sessions in the queue",
                    t,
                ),
            )],
        ),
        block(
            "crew",
            "Crew",
            "Crew members and the Chief that are owed a turn. They stay in the Crew section either way. Off by default.",
            t,
            vec![row_card(
                "Put the crew in the queue",
                "A crew member or the Chief that is owed a turn also waits in the queue. Its row stays in the Crew section.",
                t,
                setting_switch(state, CREW, "Put the crew in the queue", t),
            )],
        ),
        block(
            "muted-repositories",
            "Muted Repositories",
            "Repositories hidden from the attention queue.",
            t,
            vec![muted_list(
                "repo",
                "Muted Repositories",
                muted_repos(state),
                "No muted repositories",
                t,
                unmute_repo,
            )],
        ),
        block(
            "muted-authors",
            "Muted Authors",
            "Authors hidden from the attention queue.",
            t,
            vec![muted_list(
                "author",
                "Muted Authors",
                muted_authors(state),
                "No muted authors",
                t,
                unmute_author,
            )],
        ),
    ]
}

/// Whether auto-settle is on, and how many repositories and authors are muted.
pub(super) fn pills(state: &State, t: &Tokens) -> Vec<El> {
    let on = setting::on(&state.facts.settings, AUTO_SETTLE);
    let (settle, tone) = if on {
        ("auto-settle on", Tone::Good)
    } else {
        ("auto-settle off", Tone::Plain)
    };
    vec![
        pill(
            "settings.pill.hygiene.auto-settle".into(),
            settle.into(),
            tone,
            t,
        ),
        pill(
            "settings.pill.hygiene.repos".into(),
            format!("{} repos", muted_repos(state).len()),
            Tone::Plain,
            t,
        ),
        pill(
            "settings.pill.hygiene.authors".into(),
            format!("{} authors", muted_authors(state).len()),
            Tone::Plain,
            t,
        ),
    ]
}
