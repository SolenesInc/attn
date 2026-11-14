//! The attention drawer: the agents waiting for the user, then the pull requests the user was
//! asked to review and the user's own, each with Approve, Merge and Mute.

use std::collections::{HashMap, HashSet};

use attn_client::{Facts, REFRESH_PRS_ID, pr_action_id};
use attn_protocol::{
    ApprovePRMessage, MergePRMessage, MutePRMessage, PR, RefreshPRsMessage, Session,
};
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};

use crate::State;
use crate::requests::{Answer, Refused};
use crate::setting;
use crate::shell::Drawer;
use crate::sidebar::model::{AgentRow, AgentState};
use crate::sidebar::queue;

pub(crate) const DRAWER: &str = "attention";
const WIDTH: f32 = 360.0;
const DONE_SHOWS_SECONDS: f64 = 1.5;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
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

#[derive(Default)]
pub(crate) struct Attention {
    acts: HashMap<(Act, String), Phase>,
    /// The pull request whose merge waits for the user's yes: its id and number.
    merging: Option<(String, i64)>,
    /// Pull requests the user muted, hidden until the daemon reports them muted or gone: Mute
    /// toggles, so a second press on a row still shown would unmute it.
    muting: HashSet<String>,
}

/// What the drawer lists: the agents waiting for the user, then the pull requests needing them.
struct Lists<'a> {
    sessions: Vec<&'a Session>,
    review: Vec<&'a PR>,
    yours: Vec<&'a PR>,
}

impl<'a> Lists<'a> {
    fn of(facts: &'a Facts, muting: &HashSet<String>) -> Lists<'a> {
        let settings = &facts.settings;
        let queue_flow = setting::on(settings, queue::QUEUE_MODE);
        let crew_in_queue = setting::on(settings, queue::QUEUE_CREW);
        let places = queue::Places::of(facts);
        let sessions = places
            .agents()
            .filter(|s| s.command_terminal != Some(true))
            .filter(|s| !crate::asks::offline(s, &facts.outposts))
            .filter(|s| {
                if queue_flow {
                    queue::participates(s, crew_in_queue) && s.turn_owed == Some(true)
                } else {
                    matches!(
                        AgentState::parse(&s.state),
                        AgentState::WaitingInput
                            | AgentState::PendingApproval
                            | AgentState::Unknown
                    )
                }
            })
            .collect();
        let pulls = &facts.pull_requests;
        let needing: Vec<&PR> = pulls
            .prs
            .iter()
            .filter(|p| !p.muted && !muting.contains(&p.id))
            .filter(|p| !pulls.repos.iter().any(|r| r.repo == p.repo && r.muted))
            .filter(|p| {
                !pulls
                    .authors
                    .iter()
                    .any(|a| a.author == p.author && a.muted)
            })
            .filter(|p| !p.approved_by_me || p.has_new_changes)
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

/// Lets a done action's check lapse when its time is up, and forgets a mute the daemon has
/// taken.
pub(crate) fn poll(state: &mut State, now: f64) -> bool {
    let attention = &mut state.window.attention;
    let before = attention.acts.len();
    attention
        .acts
        .retain(|_, phase| !matches!(phase, Phase::Done(until) if now >= *until));
    let prs = &state.facts.pull_requests.prs;
    attention
        .muting
        .retain(|id| prs.iter().any(|p| &p.id == id && !p.muted));
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

fn ask_merge(state: &mut State, pr: &str, number: i64) {
    state.window.attention.merging = Some((pr.to_owned(), number));
}

fn confirm_merge(state: &mut State, tx: &mut Tx) {
    if let Some(pr) = end_merge(state, tx) {
        act(state, Act::Merge, &pr, tx);
    }
}

/// Closes the merge confirmation and hands the keyboard back to the button that opened it.
fn end_merge(state: &mut State, tx: &mut Tx) -> Option<String> {
    let (pr, _) = state.window.attention.merging.take()?;
    tx.focus(format!("attention.pr.{pr}.merge"), true);
    Some(pr)
}

fn cancel_merge(state: &mut State, tx: &mut Tx) {
    end_merge(state, tx);
}

pub(crate) fn drawer(state: &State, t: &Tokens) -> El {
    let lists = Lists::of(&state.facts, &state.window.attention.muting);
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
            "sessions",
            "Sessions Needing Attention",
            lists.sessions.len(),
            lists.sessions.iter().map(|s| session_row(s, t)).collect(),
            t,
        ));
    }
    if !lists.review.is_empty() {
        body = body.child(section(
            "review",
            "Review Requested",
            lists.review.len(),
            lists
                .review
                .iter()
                .map(|p| pr_row(state, p, false, t))
                .collect(),
            t,
        ));
    }
    if !lists.yours.is_empty() {
        body = body.child(section(
            "yours",
            "Your PRs",
            lists.yours.len(),
            lists
                .yours
                .iter()
                .map(|p| pr_row(state, p, true, t))
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
        .key(format!("attention.{key}"))
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

fn session_row(session: &Session, t: &Tokens) -> El {
    let agent = AgentRow::of(session, false);
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

fn repo_name(repo: &str) -> &str {
    repo.rsplit('/').next().unwrap_or(repo)
}

fn pr_row(state: &State, pr: &PR, yours: bool, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let key = format!("attention.pr.{}", pr.id);
    let url = pr.url.clone();
    let heading = format!("{} #{}", repo_name(&pr.repo), pr.number);
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
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_url(state, &url, tx))
        .build(k)
        .key(format!("{key}.open"));
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
        GlyphButton::new(glyph, label)
            .help(label)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match chosen {
                Act::Approve => act(state, Act::Approve, &id, tx),
                Act::Merge => ask_merge(state, &id, number),
            })
            .build(k)
            .key(format!("{key}.{}", chosen.wire()))
    };
    let mute_id = pr.id.clone();
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
        .child(action(Act::Approve))
        .child(action(Act::Merge))
        .child(
            GlyphButton::new(icon::BELL_SLASH, "Mute this PR")
                .help("Mute this PR")
                .on_press(move |state: &mut State| mute(state, &mute_id))
                .build(k)
                .key(format!("{key}.mute")),
        );
    col()
        .key(key)
        .role("group")
        .label(heading)
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .child(open)
        .child(title)
        .child(footer)
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
    let Some((_, number)) = &state.window.attention.merging else {
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
