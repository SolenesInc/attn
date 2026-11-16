//! The attention drawer: the agents waiting for the user, then the pull requests the user was
//! asked to review and the user's own, each with Approve, Merge and Mute. Home lists the same
//! pull requests in its Pull Requests card, as the Tauri app's dashboard does.

use std::collections::{HashMap, HashSet};

use attn_client::{Facts, REFRESH_PRS_ID, pr_action_id};
use attn_protocol::{
    ApprovePRMessage, MergePRMessage, MuteAuthorMessage, MutePRMessage, MuteRepoMessage, PR,
    PRVisitedMessage, RefreshPRsMessage, Session,
};
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, ScrollSpec, Tx, col, el, memo, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};

use crate::State;
use crate::requests::{Answer, Refused};
use crate::shell::Drawer;
use crate::sidebar::model::AgentRow;
use crate::sidebar::queue;

pub(crate) const DRAWER: &str = "attention";
const WIDTH: f32 = 360.0;
const DONE_SHOWS_SECONDS: f64 = 1.5;

/// Where a pull request row shows. Home's rows are the Tauri dashboard's: opening one marks it
/// visited, they show whether it is approved, updated or has CI, and they start a worktree
/// session or mute its author. The drawer's rows are its compact ones.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Place {
    Drawer,
    Home,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
enum Act {
    Approve,
    Merge,
}

impl Act {
    fn wire(self) -> &'static str {
        match self {
            Act::Approve => "approve",
            Act::Merge => "merge",
        }
    }

    /// The glyph and name of its button while it is in `phase`.
    fn shown(self, phase: Option<Phase>) -> (&'static str, &'static str) {
        match (self, phase) {
            (Act::Approve, None) => (icon::CHECK, "Approve"),
            (Act::Approve, Some(Phase::Busy)) => ("…", "Approving"),
            (Act::Approve, Some(Phase::Done(_))) => (icon::CHECK, "Approved"),
            (Act::Merge, None) => (icon::MERGE, "Merge"),
            (Act::Merge, Some(Phase::Busy)) => ("…", "Merging"),
            (Act::Merge, Some(Phase::Done(_))) => (icon::CHECK, "Merged"),
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
enum Phase {
    Busy,
    /// Done, showing its check until the clock reads this.
    Done(f64),
}

struct Merging {
    pr: String,
    number: i64,
    /// The Merge button that asked, to take the keyboard back.
    button: String,
}

#[derive(Default)]
pub(crate) struct Attention {
    acts: HashMap<(Act, String), Phase>,
    /// The pull request whose merge waits for the user's yes.
    merging: Option<Merging>,
    /// Pull requests the user muted, hidden until the daemon reports them muted or gone: Mute
    /// toggles, so a second press on a row still shown would unmute it.
    muting: HashSet<String>,
    /// Pull requests merged here, gone from the lists once their check has shown, until the
    /// daemon stops reporting them.
    merged: HashSet<String>,
    /// The repositories whose review requests home's card folds away.
    folded_repos: HashSet<String>,
}

/// What the drawer lists: the agents waiting for the user, then the pull requests needing them.
struct Lists<'a> {
    sessions: Vec<&'a Session>,
    review: Vec<&'a PR>,
    yours: Vec<&'a PR>,
}

impl<'a> Lists<'a> {
    fn of(facts: &'a Facts, attention: &Attention) -> Lists<'a> {
        let sessions = queue::wanting(facts);
        let needing: Vec<&PR> = active(facts, attention)
            .into_iter()
            .filter(|p| needs_the_user(p))
            .collect();
        Lists {
            sessions,
            review: needing
                .iter()
                .copied()
                .filter(|p| p.role == "reviewer")
                .collect(),
            yours: needing
                .iter()
                .copied()
                .filter(|p| p.role == "author")
                .collect(),
        }
    }

    fn total(&self) -> usize {
        self.sessions.len() + self.review.len() + self.yours.len()
    }
}

/// The pull requests nobody muted, not the pull request, its repository or its author, and not
/// merged here.
fn active<'a>(facts: &'a Facts, attention: &Attention) -> Vec<&'a PR> {
    let pulls = &facts.pull_requests;
    pulls
        .prs
        .iter()
        .filter(|p| !p.muted && !attention.muting.contains(&p.id))
        .filter(|p| !attention.merged.contains(&p.id))
        .filter(|p| !pulls.repos.iter().any(|r| r.repo == p.repo && r.muted))
        .filter(|p| {
            !pulls
                .authors
                .iter()
                .any(|a| a.author == p.author && a.muted)
        })
        .collect()
}

fn needs_the_user(pr: &PR) -> bool {
    !pr.approved_by_me || pr.has_new_changes
}

/// Asks the daemon to poll GitHub for the pull requests now; a failed poll says why.
pub(crate) fn refresh(state: &mut State, tx: &mut Tx) {
    state.act_as(
        REFRESH_PRS_ID.to_owned(),
        &RefreshPRsMessage::new(),
        Refused::Prefixed("Refresh failed"),
        |_, _, _| {},
        tx,
    );
}

pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if state.window.shows(Drawer::Attention) {
        close(state, tx);
    } else {
        open(state, tx);
    }
}

/// Opens the drawer and takes the keyboard into it.
pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    state.window.open_drawer(Drawer::Attention);
    tx.focus(DRAWER, false);
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    state.window.close_drawer(Drawer::Attention);
    tx.blur();
}

fn act(state: &mut State, act: Act, pr: &str, tx: &mut Tx) {
    let key = (act, pr.to_owned());
    if state.window.attention.acts.contains_key(&key) {
        return;
    }
    state.window.attention.acts.insert(key.clone(), Phase::Busy);
    let id = pr_action_id(act.wire(), pr);
    let answered = move |state: &mut State, answer: Answer, tx: &mut Tx| match answer.outcome() {
        Ok(_) => {
            let until = tx.clock() + DONE_SHOWS_SECONDS;
            state.window.attention.acts.insert(key, Phase::Done(until));
            tx.wake_at(until);
        }
        Err(reason) => {
            state.window.attention.acts.remove(&key);
            state.window.notify(reason, tx);
        }
    };
    let pr = pr.to_owned();
    match act {
        Act::Approve => state.ask_as(
            id,
            &ApprovePRMessage {
                id: pr,
                ..ApprovePRMessage::new()
            },
            answered,
            tx,
        ),
        Act::Merge => state.ask_as(
            id,
            &MergePRMessage {
                id: pr,
                method: "squash".into(),
                ..MergePRMessage::new()
            },
            answered,
            tx,
        ),
    }
}

/// Lets a done action's check lapse when its time is up, a merged pull request with it, and
/// forgets a mute the daemon has taken and a merge the daemon no longer reports.
pub(crate) fn poll(state: &mut State, now: f64) -> bool {
    let attention = &mut state.window.attention;
    let before = attention.acts.len();
    let mut merged = Vec::new();
    attention.acts.retain(|(act, pr), phase| {
        let lapsed = matches!(phase, Phase::Done(until) if now >= *until);
        if lapsed && *act == Act::Merge {
            merged.push(pr.clone());
        }
        !lapsed
    });
    attention.merged.extend(merged);
    let prs = &state.facts.pull_requests.prs;
    attention
        .muting
        .retain(|id| prs.iter().any(|p| &p.id == id && !p.muted));
    attention
        .merged
        .retain(|id| prs.iter().any(|p| &p.id == id));
    attention.acts.len() != before
}

fn mute(state: &mut State, pr: &str) {
    if !state.window.attention.muting.insert(pr.to_owned()) {
        return;
    }
    let _ = state.daemon.send(&MutePRMessage {
        id: pr.to_owned(),
        ..MutePRMessage::new()
    });
}

fn ask_merge(state: &mut State, pr: &str, number: i64, button: String) {
    state.window.attention.merging = Some(Merging {
        pr: pr.to_owned(),
        number,
        button,
    });
}

fn confirm_merge(state: &mut State, tx: &mut Tx) {
    if let Some(pr) = end_merge(state, tx) {
        act(state, Act::Merge, &pr, tx);
    }
}

/// Closes the merge confirmation and hands the keyboard back to the button that opened it.
fn end_merge(state: &mut State, tx: &mut Tx) -> Option<String> {
    let Merging { pr, button, .. } = state.window.attention.merging.take()?;
    tx.focus(button, true);
    Some(pr)
}

fn cancel_merge(state: &mut State, tx: &mut Tx) {
    end_merge(state, tx);
}

pub(crate) fn drawer(state: &State, t: &Tokens) -> El {
    let lists = Lists::of(&state.facts, &state.window.attention);
    let total = lists.total();
    let k = attn_ui::kit(t);
    let mut body = col()
        .key("attention.body")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .gap(space::S3)
        .padding(space::S3)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    if !lists.sessions.is_empty() {
        body = body.child(section(
            "attention.sessions",
            "Sessions Needing Attention",
            lists.sessions.len(),
            lists
                .sessions
                .iter()
                .map(|s| session_row(s, &state.facts.outposts, t))
                .collect(),
            t,
        ));
    }
    if !lists.review.is_empty() {
        body = body.child(section(
            "attention.review",
            "Review Requested",
            lists.review.len(),
            lists
                .review
                .iter()
                .map(|p| {
                    pr_row(
                        state,
                        p,
                        Place::Drawer,
                        false,
                        format!("attention.pr.{}", p.id),
                        named(p),
                        t,
                    )
                })
                .collect(),
            t,
        ));
    }
    if !lists.yours.is_empty() {
        body = body.child(section(
            "attention.yours",
            "Your PRs",
            lists.yours.len(),
            lists
                .yours
                .iter()
                .map(|p| {
                    pr_row(
                        state,
                        p,
                        Place::Drawer,
                        true,
                        format!("attention.pr.{}", p.id),
                        named(p),
                        t,
                    )
                })
                .collect(),
            t,
        ));
    }
    if total == 0 {
        body = body.child(
            text("Nothing needs attention")
                .key("attention.empty")
                .role("status")
                .font(type_::BODY)
                .color(t.text_tertiary),
        );
    }
    col()
        .key(DRAWER)
        .role("region")
        .label("Needs Attention")
        .focusable(true)
        .on_key(drawer_key)
        .w(WIDTH)
        .h_full()
        .min_h(0.0)
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
        .child(
            row()
                .key("attention.header")
                .w_full()
                .items_center()
                .gap(space::S2)
                .px(space::S3)
                .py(space::S2)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
                .child(
                    text("Needs Attention")
                        .font(type_::SECTION)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(
                    text(total.to_string())
                        .key("attention.count")
                        .role("status")
                        .label(format!("{total} need attention"))
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
                .child(
                    GlyphButton::new(icon::CLOSE, "Close")
                        .help("Close (Esc)")
                        .on_press_tx(close)
                        .build(k)
                        .key("attention.close"),
                ),
        )
        .child(body)
}

fn section(key: &str, title: &str, count: usize, rows: Vec<El>, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("group")
        .label(title)
        .w_full()
        .gap(space::S1)
        .child(
            row()
                .gap(space::S2)
                .child(text(title).font(type_::LABEL).color(t.text_secondary))
                .child(
                    text(count.to_string())
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                ),
        )
        .children(rows)
}

fn session_row(session: &Session, outposts: &[attn_protocol::OutpostInfo], t: &Tokens) -> El {
    let agent = AgentRow::of(session, outposts, false);
    let key = format!("attention.session.{}", session.id);
    let id = session.id.clone();
    Button::new(agent.name.clone())
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label_with({
            let mark_key = key.clone();
            let agent = agent.clone();
            let t = *t;
            move |font, ink| {
                row()
                    .w_full()
                    .gap(space::S2)
                    .items_center()
                    .child(crate::sidebar::state_mark(&mark_key, &agent, &t))
                    .child(text(agent.name.clone()).font(font).color(ink).truncate())
            }
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            crate::sidebar::open_agent(state, &id, tx)
        })
        .build(attn_ui::kit(t))
        .key(key)
        .w_full()
}

/// A pull request by its repository's name and number: "attn #20".
fn named(pr: &PR) -> String {
    format!("{} #{}", repo_name(&pr.repo), pr.number)
}

fn repo_name(repo: &str) -> &str {
    repo.rsplit('/').next().unwrap_or(repo)
}

fn pr_row(
    state: &State,
    pr: &PR,
    place: Place,
    yours: bool,
    key: String,
    heading: String,
    t: &Tokens,
) -> El {
    let k = attn_ui::kit(t);
    let url = pr.url.clone();
    let visited = (place == Place::Home).then(|| pr.id.clone());
    let open = Button::new(format!("{heading}: {}", pr.title))
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .help(format!("Open {heading} on GitHub"))
        .label_with({
            let heading = heading.clone();
            move |font, ink| {
                row()
                    .grow(1.0)
                    .child(text(heading.clone()).font(font).color(ink))
            }
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            if let Some(id) = &visited {
                visit(state, id);
            }
            open_url(state, &url, tx)
        })
        .build(k)
        .key(format!("{key}.open"))
        .grow(1.0);
    let top = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .child(open)
        .children(
            (place == Place::Home)
                .then(|| marks(pr, &key, t))
                .into_iter()
                .flatten(),
        );
    let title = text(pr.title.clone())
        .font(type_::BODY)
        .color(t.text_primary)
        .wrap()
        .w_full()
        .a11y_hidden();
    let phase = |act: Act| {
        state
            .window
            .attention
            .acts
            .get(&(act, pr.id.clone()))
            .copied()
    };
    let number = pr.number;
    let action = |chosen: Act| {
        let (glyph, label) = chosen.shown(phase(chosen));
        let id = pr.id.clone();
        let button = format!("{key}.{}", chosen.wire());
        let asking = button.clone();
        GlyphButton::new(glyph, label)
            .help(label)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match chosen {
                Act::Approve => act(state, Act::Approve, &id, tx),
                Act::Merge => ask_merge(state, &id, number, asking.clone()),
            })
            .build(k)
            .key(button)
    };
    let mute_id = pr.id.clone();
    let home = place == Place::Home;
    let new_session = home.then(|| {
        let pr = pr.clone();
        GlyphButton::new(icon::POP_OUT, "New worktree session")
            .help("New worktree session")
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                crate::picker::pull_request::open_in_worktree(state, &pr, tx)
            })
            .build(k)
            .key(format!("{key}.new"))
    });
    let mute_author = (home && !pr.author.is_empty()).then(|| {
        let author = pr.author.clone();
        Button::new(author.clone())
            .variant(ButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .help(format!("Mute all PRs by {author}"))
            .on_press(move |state: &mut State| mute_author(state, &author))
            .build(k)
            .key(format!("{key}.mute-author"))
            .label(format!("Mute all PRs by {}", pr.author))
    });
    let footer = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .child(
            text(if yours {
                pr.reason.replace('_', " ")
            } else {
                String::new()
            })
            .key(format!("{key}.reason"))
            .font(type_::LABEL)
            .color(t.text_tertiary)
            .grow(1.0),
        )
        .children(new_session)
        .child(action(Act::Approve))
        .child(action(Act::Merge))
        .child(
            GlyphButton::new(icon::BELL_SLASH, "Mute this PR")
                .help("Mute this PR")
                .on_press(move |state: &mut State| mute(state, &mute_id))
                .build(k)
                .key(format!("{key}.mute")),
        )
        .children(mute_author);
    let settled = home && pr.approved_by_me && !pr.has_new_changes;
    col()
        .key(key)
        .role("group")
        .label(heading)
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .opacity(if settled { 0.5 } else { 1.0 })
        .child(top)
        .child(title)
        .child(footer)
}

/// What changed on a pull request at a glance: new commits or comments since the user's last
/// visit, the user's approval, and how its CI went.
fn marks(pr: &PR, key: &str, t: &Tokens) -> Vec<El> {
    let mut marks = Vec::new();
    if pr.has_new_changes {
        marks.push(
            text("UPDATED")
                .key(format!("{key}.updated"))
                .role("status")
                .label("Updated")
                .help("New commits/comments since your last visit")
                .font(type_::SECTION)
                .color(t.accent)
                .px(space::S1)
                .radius(3.0)
                .fill(t.bg_selected),
        );
    }
    if pr.approved_by_me {
        marks.push(
            text(icon::CHECK)
                .key(format!("{key}.approved"))
                .role("status")
                .label("Approved")
                .help("You approved this PR")
                .font(type_::LABEL)
                .color(t.success),
        );
    }
    let ci = pr
        .ci_status
        .as_deref()
        .filter(|s| !s.is_empty() && *s != "none");
    if let Some(ci) = ci {
        let color = match ci {
            "success" => t.success,
            "failure" => t.error,
            "pending" => t.warning,
            _ => t.text_tertiary,
        };
        marks.push(
            el().key(format!("{key}.ci"))
                .role("status")
                .label(format!("CI {ci}"))
                .help(format!("CI {ci}"))
                .w(6.0)
                .h(6.0)
                .radius(3.0)
                .fill(color),
        );
    }
    marks
}

/// Tells the daemon the user looked at the pull request, which clears its new changes.
fn visit(state: &mut State, pr: &str) {
    let _ = state.daemon.send(&PRVisitedMessage {
        id: pr.to_owned(),
        ..PRVisitedMessage::new()
    });
}

fn mute_author(state: &mut State, author: &str) {
    let _ = state.daemon.send(&MuteAuthorMessage {
        author: author.to_owned(),
        ..MuteAuthorMessage::new()
    });
}

fn mute_repo(state: &mut State, repo: &str) {
    let _ = state.daemon.send(&MuteRepoMessage {
        repo: repo.to_owned(),
        ..MuteRepoMessage::new()
    });
}

fn fold_repo(state: &mut State, repo: &str) {
    let folded = &mut state.window.attention.folded_repos;
    if !folded.remove(repo) {
        folded.insert(repo.to_owned());
    }
}

/// Home's Pull Requests card: the user's own pull requests, then the review requests by
/// repository, each repository folding away or muted from its header.
pub(crate) fn home_card(state: &State, t: &Tokens) -> El {
    let attention = &state.window.attention;
    let mut acts: Vec<(Act, String, Option<u64>)> = attention
        .acts
        .iter()
        .map(|((act, pr), phase)| {
            let done = match phase {
                Phase::Busy => None,
                Phase::Done(until) => Some(until.to_bits()),
            };
            (*act, pr.clone(), done)
        })
        .collect();
    acts.sort();
    let mut muting: Vec<&String> = attention.muting.iter().collect();
    muting.sort();
    let mut folded: Vec<&String> = attention.folded_repos.iter().collect();
    folded.sort();
    let mut merged: Vec<&String> = attention.merged.iter().collect();
    merged.sort();
    let reads = (
        std::sync::Arc::as_ptr(&state.facts.pull_requests) as usize,
        std::sync::Arc::as_ptr(&state.facts.github) as usize,
        std::sync::Arc::as_ptr(&state.keymap) as usize,
        acts,
        (muting, merged),
        folded,
        *t,
    );
    memo("home.prs", reads, || pr_card(state, t))
}

fn pr_card(state: &State, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let facts = &state.facts;
    let attention = &state.window.attention;
    let active = active(facts, attention);
    let needing = active.iter().filter(|p| needs_the_user(p)).count();
    let yours: Vec<&PR> = active
        .iter()
        .copied()
        .filter(|p| p.role == "author")
        .collect();
    let mut repos: Vec<(&str, Vec<&PR>)> = Vec::new();
    for pr in active.iter().copied().filter(|p| p.role != "author") {
        match repos.iter_mut().find(|(repo, _)| *repo == pr.repo) {
            Some((_, prs)) => prs.push(pr),
            None => repos.push((&pr.repo, vec![pr])),
        }
    }
    let mut body = col()
        .key("home.prs.body")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .gap(space::S3)
        .scroll(ScrollSpec {
            stick_bottom: false,
        });
    let empty = |said: String| {
        text(said)
            .key("home.prs.empty")
            .role("status")
            .font(type_::BODY)
            .color(t.text_muted)
            .wrap()
            .w_full()
    };
    if let Some(reason) = &facts.github.polling_off_reason {
        body = body.child(empty(reason.clone()));
    } else if active.is_empty() {
        body = body.child(empty("No PRs need attention".into()));
    } else {
        if !yours.is_empty() {
            body = body.child(section(
                "home.prs.yours",
                "Yours",
                yours.len(),
                yours
                    .iter()
                    .map(|p| {
                        pr_row(
                            state,
                            p,
                            Place::Home,
                            true,
                            format!("home.pr.{}", p.id),
                            named(p),
                            t,
                        )
                    })
                    .collect(),
                t,
            ));
        }
        if !repos.is_empty() {
            let count = repos.iter().map(|(_, prs)| prs.len()).sum();
            let groups = repos
                .iter()
                .map(|(repo, prs)| repo_group(state, repo, prs, t))
                .collect();
            body = body.child(section(
                "home.prs.review",
                "Review requested",
                count,
                groups,
                t,
            ));
        }
    }
    let refresh_help = match crate::palette::label_of(state, &crate::Action::SessionRefreshPrs) {
        Some(keys) => format!("Refresh PRs ({keys})"),
        None => "Refresh PRs".to_owned(),
    };
    col()
        .key("home.prs")
        .role("region")
        .label("Pull Requests")
        .grow(1.0)
        .min_w(300.0)
        .min_h(0.0)
        .padding(space::S3)
        .gap(space::S3)
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .child(
                    text("Pull Requests")
                        .role("heading")
                        .font(type_::SECTION)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(
                    GlyphButton::new(icon::REFRESH, "Refresh PRs")
                        .help(refresh_help)
                        .on_press_tx(refresh)
                        .build(k)
                        .key("home.prs.refresh"),
                )
                .children(facts.github.polling_off_reason.is_none().then(|| {
                    text(needing.to_string())
                        .key("home.prs.count")
                        .role("status")
                        .label(format!("{needing} need attention"))
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                })),
        )
        .child(body)
}

fn repo_group(state: &State, repo: &str, prs: &[&PR], t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let folded = state.window.attention.folded_repos.contains(repo);
    let key = format!("home.prs.repo.{repo}");
    let name = repo_name(repo);
    let (fold_repo_id, mute_repo_id) = (repo.to_owned(), repo.to_owned());
    let header = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .child(
            Button::new(format!("{name}, {} review", prs.len()))
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .label_with({
                    let said = format!(
                        "{} {name}  {} review",
                        if folded {
                            icon::CARET_RIGHT
                        } else {
                            icon::CARET_DOWN
                        },
                        prs.len()
                    );
                    move |font, ink| {
                        row()
                            .grow(1.0)
                            .child(text(said.clone()).font(font).color(ink))
                    }
                })
                .on_press(move |state: &mut State| fold_repo(state, &fold_repo_id))
                .build(k)
                .key(format!("{key}.fold"))
                .a11y_value(if folded { "collapsed" } else { "expanded" })
                .grow(1.0),
        )
        .child(
            GlyphButton::new(icon::BAN, "Mute all PRs from this repo")
                .help("Mute all PRs from this repo")
                .on_press(move |state: &mut State| mute_repo(state, &mute_repo_id))
                .build(k)
                .key(format!("{key}.mute")),
        );
    col()
        .key(key)
        .role("group")
        .label(name.to_owned())
        .w_full()
        .gap(space::S1)
        .child(header)
        .children(
            (!folded)
                .then(|| {
                    prs.iter()
                        .map(|p| {
                            pr_row(
                                state,
                                p,
                                Place::Home,
                                false,
                                format!("home.pr.{}", p.id),
                                format!("#{}", p.number),
                                t,
                            )
                        })
                        .collect::<Vec<_>>()
                })
                .into_iter()
                .flatten(),
        )
}

fn open_url(state: &mut State, url: &str, tx: &mut Tx) {
    if let Err(why) = tx.open_url(url) {
        state
            .window
            .notify(format!("Could not open {url}: {why}"), tx);
    }
}

/// The yes the user gives before a merge, over the window.
pub(crate) fn merge_confirmation(state: &State, t: &Tokens) -> Vec<El> {
    let Some(Merging { number, .. }) = &state.window.attention.merging else {
        return Vec::new();
    };
    let k = attn_ui::kit(t);
    let mut layer = Vec::new();
    Dialog::new("attention.merge", format!("Merge PR #{number}?"))
        .description("This will merge the pull request and delete the branch.")
        .open(true)
        .fit()
        .on_close_tx(cancel_merge)
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .padding(space::S3)
                .child(row().grow(1.0))
                .child(
                    Button::new("Cancel")
                        .on_press_tx(cancel_merge)
                        .build(k)
                        .key("attention.merge.cancel"),
                )
                .child(
                    Button::new("Merge")
                        .variant(ButtonVariant::Submit)
                        .on_press_tx(confirm_merge)
                        .build(k)
                        .key("attention.merge.confirm"),
                ),
        )
        .build(k, &mut layer);
    layer
}

fn drawer_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key == Key::Escape {
        crate::shell::escape_drawers(state, tx);
        return true;
    }
    false
}
