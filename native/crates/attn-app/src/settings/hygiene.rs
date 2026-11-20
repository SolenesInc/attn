//! Settings → Attention queue: auto-settle and its two windows, which agents join the queue,
//! and the repositories and authors muted out of it.

use std::collections::BTreeSet;

use attn_protocol::{MuteAuthorMessage, MuteRepoMessage};
use attn_ui::Tokens;
use rune::El;

use attn_ui::system::{Button, ListRow, Reveal, Size, Tone, tag};

use attn_ui::blocks::{Block, List, Section, SettingRow};

use super::controls::{SettingField, setting_switch};
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
) -> Block {
    List::new(format!("settings.muted.{kind}"), title)
        .rows(names.into_iter().map(|name| {
            let pressed = name.clone();
            ListRow::new(format!("settings.muted.{kind}.{name}"), name.clone())
                .still()
                .title(name.clone())
                .controls(
                    [Button::new("Unmute")
                        .size(Size::Compact)
                        .on_press_tx(move |state: &mut State, _: &mut rune::Tx| {
                            unmute(state, &pressed)
                        })
                        .build(t)
                        .key(format!("settings.unmute.{kind}.{name}"))
                        .label("Unmute")
                        .into()],
                    Reveal::Always,
                )
                .build(t)
        }))
        .empty(format!("settings.muted.{kind}.empty"), empty, None)
        .build(t)
}

fn shortcut(state: &State, action: &Action) -> String {
    state
        .keymap
        .binding(action)
        .map(|b| state.keymap.label(b))
        .unwrap_or_default()
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let settle = shortcut(state, &Action::SessionSettle);
    let keep = shortcut(state, &Action::SessionCancelCountdown);
    let switch_row = |key: &'static str, title: &str, copy: &str| {
        SettingRow::new(
            format!("settings.row.{key}"),
            title.to_owned(),
            copy.to_owned(),
            setting_switch(state, key, title, t),
        )
        .build(t)
    };
    vec![
        Section::new("settings.block.auto-settle", "Auto-settle")
            .description("Closes a turn for you once you have steered the agent and walked away, so the queue does not fill up with turns you have already dealt with. Off by default.")
            .push(switch_row(
                AUTO_SETTLE,
                "Settle a turn once you have steered the agent",
                &format!(
                    "When an agent you owe a turn goes back to work and stays there, its terminal tile runs a countdown and then settles the turn for you — the same thing {settle} does. Press {keep} to keep the turn instead. Anything that makes the agent want you again — a question, an approval, an error, a finished run — cancels it. Off by default."
                ),
            ))
            .push(
                SettingField::new(AUTO_SETTLE_ARM, "Wait before counting down (seconds)")
                    .step(5.0)
                    .hint("How long the agent must keep working before anything starts. Nothing is shown during this window.")
                    .build(state, t),
            )
            .push(
                SettingField::new(AUTO_SETTLE_COUNTDOWN, "Countdown before settling (seconds)")
                    .step(1.0)
                    .hint(format!(
                        "How long the countdown runs on the tile — your window to press {keep} and keep the turn."
                    ))
                    .build(state, t),
            )
            .build(t),
        Section::new("settings.block.quiet-notifications", "Quiet notifications")
            .description("Keeps agents whose run attn started only to deliver a notification from sending you to them. On by default.")
            .push(switch_row(
                QUIET_NOTIFICATIONS,
                "Quiet notifications in the queue",
                "When attn starts an agent's run to deliver a notification (a message, a seed update, a pull request update), the agent stays out of your queue when it stops, unless it needs an approval or opens its turn to ask for you. An agent that was waiting for you leaves the queue when a notification sets it working, without moving you.",
            ))
            .build(t),
        Section::new("settings.block.hidden-sessions", "Hidden sessions")
            .description("Shared Codex sessions that keep running without an attn tile. On by default.")
            .push(switch_row(
                HIDDEN_SESSIONS,
                "Show hidden sessions in the queue",
                "Switching sessions inside Codex can leave the previous session running without a tile. Include these hidden sessions in the queue; selecting one opens it in a terminal.",
            ))
            .build(t),
        Section::new("settings.block.crew", "Crew")
            .description("Crew members and the Chief that are owed a turn. They stay in the Crew section either way. Off by default.")
            .push(switch_row(
                CREW,
                "Put the crew in the queue",
                "A crew member or the Chief that is owed a turn also waits in the queue. Its row stays in the Crew section.",
            ))
            .build(t),
        Section::new("settings.block.muted-repositories", "Muted Repositories")
            .description("Repositories hidden from the attention queue.")
            .push(muted_list(
                "repo",
                "Muted Repositories",
                muted_repos(state),
                "No muted repositories",
                t,
                unmute_repo,
            ))
            .build(t),
        Section::new("settings.block.muted-authors", "Muted Authors")
            .description("Authors hidden from the attention queue.")
            .push(muted_list(
                "author",
                "Muted Authors",
                muted_authors(state),
                "No muted authors",
                t,
                unmute_author,
            ))
            .build(t),
    ]
}

/// Whether auto-settle is on, and how many repositories and authors are muted.
pub(super) fn pills(state: &State, t: &Tokens) -> Vec<El> {
    let on = setting::on(&state.facts.settings, AUTO_SETTLE);
    let (settle, tone) = if on {
        ("auto-settle on", Tone::Good)
    } else {
        ("auto-settle off", Tone::Neutral)
    };
    vec![
        El::from(tag("settings.pill.hygiene.auto-settle", settle, tone, t)).role("status"),
        El::from(tag(
            "settings.pill.hygiene.repos",
            format!("{} repos", muted_repos(state).len()),
            Tone::Neutral,
            t,
        ))
        .role("status"),
        El::from(tag(
            "settings.pill.hygiene.authors",
            format!("{} authors", muted_authors(state).len()),
            Tone::Neutral,
            t,
        ))
        .role("status"),
    ]
}
