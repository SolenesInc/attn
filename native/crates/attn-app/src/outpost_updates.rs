//! The band under the title bar that offers to bring outposts to the home's build, follows
//! each update's step, and says how it ended. A success closes on its own after a countdown
//! that holds while the pointer is over the band. Dismissing the band keeps it closed, across
//! launches, until the home's build changes or a new update starts.

use attn_client::Connection;
use attn_protocol::OutpostInfo;
use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::space;
use rune::{El, Tx, row, text};
use rune_kit::{Button, ControlSize};

use crate::State;
use crate::closing::{Closing, Tick};
use crate::outpost::{needs_update, update_text, updating};

/// The Tauri app's key, so a dismissal carries over from it.
const CLOSED_PREF: &str = "attn.outpost_updates.dismissed_home_build";
const OUTPOSTS_ENABLED: &str = "outposts_enabled";

#[derive(Default)]
pub(crate) struct Banner {
    /// What was dismissed: the home's build, or a success by the build and the outposts it
    /// updated. `None` until this window reads the last launch's dismissal.
    closed: Option<Option<String>>,
    closing: Closing,
}

/// What the band would show, before a dismissal hides it.
struct Updates<'a> {
    different: Vec<&'a OutpostInfo>,
    updated: Vec<&'a OutpostInfo>,
    /// Every update ended well and no outpost differs.
    done: bool,
    /// What a dismissal of this band remembers.
    key: String,
}

impl Updates<'_> {
    fn pending(&self) -> bool {
        !self.different.is_empty() || (!self.updated.is_empty() && !self.done)
    }

    fn busy(&self) -> bool {
        self.updated.iter().any(|o| updating(o))
    }
}

fn updates(state: &State) -> Option<Updates<'_>> {
    let facts = &state.facts;
    let home = facts.home_build.as_str();
    if home.is_empty() || !crate::setting::on(&facts.settings, OUTPOSTS_ENABLED) {
        return None;
    }
    let different: Vec<_> = facts
        .outposts
        .iter()
        .filter(|o| needs_update(o, home))
        .collect();
    let updated: Vec<_> = facts
        .outposts
        .iter()
        .filter(|o| o.update.is_some())
        .collect();
    let done = different.is_empty()
        && !updated.is_empty()
        && updated
            .iter()
            .all(|o| o.update.as_ref().is_some_and(|u| u.phase == "done"));
    let key = if done {
        std::iter::once(home)
            .chain(updated.iter().map(|o| o.id.as_str()))
            .collect::<Vec<_>>()
            .join("\n")
    } else {
        home.to_owned()
    };
    Some(Updates {
        different,
        updated,
        done,
        key,
    })
}

fn closed(state: &State) -> Option<String> {
    match &state.window.outpost_updates.closed {
        Some(closed) => closed.clone(),
        None => crate::frame::pref_string(CLOSED_PREF).filter(|c| !c.is_empty()),
    }
}

fn set_closed(state: &mut State, closed: Option<String>) {
    crate::frame::set_pref_string(CLOSED_PREF, closed.as_deref().unwrap_or_default());
    state.window.outpost_updates.closed = Some(closed);
}

/// The updates the band shows, unless they are dismissed or there is nothing to say.
fn shown(state: &State) -> Option<Updates<'_>> {
    let updates = updates(state)?;
    let closed = closed(state);
    let home = state.facts.home_build.as_str();
    let dismissed = closed
        .as_deref()
        .is_some_and(|c| c == home || c == updates.key);
    (!dismissed && (updates.done || updates.pending())).then_some(updates)
}

/// A new update brings back a band whose success was dismissed, and a shown success counts
/// down.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let pending = updates(state).is_some_and(|u| u.pending());
    let home = state.facts.home_build.clone();
    if pending && closed(state).is_some_and(|c| c != home) {
        set_closed(state, None);
    }
    let due = shown(state).filter(|u| u.done).map(|u| u.key);
    state.window.outpost_updates.closing.follow(due, tx);
}

pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    match state.window.outpost_updates.closing.poll(now, tx) {
        Tick::Same => false,
        Tick::Changed => true,
        Tick::RanOut => {
            close(state);
            true
        }
    }
}

fn close(state: &mut State) {
    let Some(key) = updates(state).map(|u| u.key) else {
        return;
    };
    state.window.outpost_updates.closing.stop();
    set_closed(state, Some(key));
}

fn update_all(state: &mut State, _: &mut Tx) {
    let ids: Vec<String> = updates(state)
        .into_iter()
        .flat_map(|u| u.different)
        .map(|o| o.id.clone())
        .collect();
    for id in ids {
        crate::settings::outposts::update(state, &id);
    }
}

fn headline(updates: &Updates) -> String {
    if updates.busy() {
        return "Updating outposts.".into();
    }
    let failed: Vec<&str> = updates
        .updated
        .iter()
        .filter(|o| o.update.as_ref().is_some_and(|u| u.phase == "failed"))
        .map(|o| o.name.as_str())
        .collect();
    if !failed.is_empty() {
        return format!("Couldn’t update {}.", crate::outpost::names(&failed));
    }
    if !updates.different.is_empty() {
        let (missing, other): (Vec<&OutpostInfo>, Vec<&OutpostInfo>) = updates
            .different
            .iter()
            .partition(|o| o.host_missing == Some(true));
        let names = |rows: &[&OutpostInfo]| {
            crate::outpost::names(&rows.iter().map(|o| o.name.as_str()).collect::<Vec<_>>())
        };
        let other = (!other.is_empty()).then(|| {
            let verb = if other.len() == 1 { "runs" } else { "run" };
            format!("{} {verb} a different build than home.", names(&other))
        });
        let missing = (!missing.is_empty()).then(|| {
            let verb = if missing.len() == 1 { "is" } else { "are" };
            format!(
                "{} {verb} missing the shared terminal host.",
                names(&missing)
            )
        });
        return [other, missing]
            .into_iter()
            .flatten()
            .collect::<Vec<_>>()
            .join(" ");
    }
    let updated: Vec<&str> = updates.updated.iter().map(|o| o.name.as_str()).collect();
    format!("Updated {}.", crate::outpost::names(&updated))
}

/// One note per step the unfinished updates are at, naming every outpost at it, keyed by
/// the first of them.
fn notes(updates: &Updates) -> Vec<(String, String)> {
    let mut groups: Vec<(String, Vec<&OutpostInfo>)> = Vec::new();
    let unfinished = updates
        .updated
        .iter()
        .filter(|o| o.update.as_ref().is_some_and(|u| u.phase != "done"));
    for outpost in unfinished {
        let said = update_text(outpost).unwrap_or_default();
        match groups.iter_mut().find(|(s, _)| *s == said) {
            Some((_, group)) => group.push(outpost),
            None => groups.push((said, vec![outpost])),
        }
    }
    groups
        .into_iter()
        .map(|(said, group)| {
            let names: Vec<&str> = group.iter().map(|o| o.name.as_str()).collect();
            (
                group[0].id.clone(),
                format!("{}: {said}", crate::outpost::names(&names)),
            )
        })
        .collect()
}

pub(crate) fn banner(state: &State, t: &Tokens) -> Option<El> {
    let updates = shown(state)?;
    let kit = attn_ui::kit(t);
    let connected = state.facts.connection == Connection::Connected;
    let note = |key: String, said: String| {
        text(said)
            .key(key)
            .font(type_::BODY)
            .color(t.text_secondary)
            .wrap()
            .min_w(0.0)
            .shrink()
    };
    let notes = (!updates.done)
        .then(|| notes(&updates))
        .into_iter()
        .flatten()
        .map(|(id, said)| note(format!("outpost-updates.note.{id}"), said));
    let error = crate::settings::outposts::command_error(state)
        .map(|e| note("outpost-updates.error".into(), e.to_owned()));
    let busy = updates.busy();
    let update = (!updates.different.is_empty()).then(|| {
        Button::new(if busy {
            "Updating…"
        } else {
            "Update outposts"
        })
        .control_size(ControlSize::Small)
        .disabled(!connected || busy)
        .on_press_tx(update_all)
        .build(kit)
        .key("outpost-updates.update")
    });
    let countdown = state
        .window
        .outpost_updates
        .closing
        .seconds(&updates.key)
        .filter(|_| updates.done)
        .map(|seconds| {
            text(format!("Closing in {seconds}s"))
                .key("outpost-updates.countdown")
                .font(type_::BODY)
                .color(t.text_tertiary)
        });
    let dismiss = GlyphButton::new(icon::CLOSE, "Dismiss outpost updates")
        .help("Dismiss")
        .on_press(close)
        .build(kit)
        .key("outpost-updates.dismiss");
    Some(
        row()
            .key("outpost-updates")
            .role("region")
            .label("Outpost updates")
            .w_full()
            .px(space::S3)
            .py(space::S1)
            .gap(space::S2)
            .items_center()
            .fill(t.bg_panel)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
            .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
                state.window.outpost_updates.closing.hold(inside, tx);
            })
            .child(
                text(headline(&updates))
                    .key("outpost-updates.text")
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap()
                    .min_w(0.0),
            )
            .children(notes)
            .children(error)
            .child(row().grow(1.0))
            .children(update)
            .children(countdown)
            .child(dismiss),
    )
}
