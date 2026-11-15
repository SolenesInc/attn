//! Callouts: an agent's `attn callout show` points at a part of the window with a card beside
//! it and a ring around it, one step or a tour of several. The window answers each request with
//! the anchors it shows, and refuses a callout one of whose fixed anchors it does not show.

use attn_client::Reply;
use attn_protocol::{
    CalloutAnchorsRequestMessage, CalloutResultMessage, CalloutShowRequestMessage, CalloutStep,
};
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{space, type_};
use rune::{Align, Cx, El, ElementInfo, Key, KeyEvent, Mods, Side, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

use crate::State;
use crate::action::Action;

const CARD: &str = "callout";
const CARD_WIDTH: f32 = 300.0;
const GAP: f32 = 14.0;
const RING_OUTSET: f32 = 3.0;

const FIXED: [&str; 9] = [
    "sidebar",
    "home",
    "commands",
    "garden",
    "asks",
    "desktops",
    "queue",
    "crew",
    "settings-button",
];

pub(crate) struct Callout {
    profile: String,
    from: String,
    steps: Vec<CalloutStep>,
    index: usize,
    /// Another layer covers the window: a tour waits under it, and a one-step callout ends.
    hidden: bool,
    /// A press elsewhere let the window have the keys back, until the card is pressed again.
    released: bool,
}

impl Callout {
    fn tour(&self) -> bool {
        self.steps.len() > 1
    }

    fn step(&self) -> &CalloutStep {
        &self.steps[self.index]
    }

    fn last(&self) -> bool {
        self.index + 1 == self.steps.len()
    }
}

/// The element keys a fixed anchor may show as, in the order the window prefers them.
fn fixed_keys(anchor: &str) -> &'static [&'static str] {
    match anchor {
        "sidebar" => &["sidebar", "rail", "queue-bar"],
        "home" => &["sidebar.home", "rail.home"],
        "commands" => &["titlebar.commands"],
        "garden" => &["titlebar.garden"],
        "asks" => &["titlebar.asks"],
        "desktops" => &[
            "sidebar.desktops",
            "queue.desktops",
            "rail.desktops",
            "queue-bar.desktops",
        ],
        "queue" => &["sidebar.queue", "queue-bar"],
        "settings-button" => &["home.settings"],
        _ => &[],
    }
}

fn is_fixed(anchor: &str) -> bool {
    anchor != "agent" && anchor != "desktop"
}

fn step_keys(state: &State, step: &CalloutStep) -> Vec<String> {
    match step.anchor.as_str() {
        "agent" => {
            let id = step.session_id.clone().unwrap_or_default();
            let mut keys: Vec<String> = crate::sidebar::queue_rows(state)
                .into_iter()
                .filter(|row| row.session == id)
                .map(|row| row.key)
                .collect();
            keys.push(format!("sidebar.session.{id}"));
            keys
        }
        "desktop" => vec![format!(
            "sidebar.desktop.{}",
            step.desktop_id.clone().unwrap_or_default()
        )],
        fixed => fixed_keys(fixed).iter().map(|k| (*k).to_owned()).collect(),
    }
}

fn shows(info: &Option<ElementInfo>) -> bool {
    info.as_ref()
        .is_some_and(|e| !e.inert && e.opacity > 0.0 && e.visible[2] > 0.0 && e.visible[3] > 0.0)
}

/// Answers a `callout_show_request` or `callout_anchors_request`; false for any other reply.
pub(crate) fn receive(state: &mut State, reply: &Reply, tx: &mut Tx) -> bool {
    match reply.event.as_str() {
        "callout_anchors_request" => {
            if let Some(request) = reply.parse::<CalloutAnchorsRequestMessage>() {
                answer_anchors(state, request, tx);
            }
            true
        }
        "callout_show_request" => {
            if let Some(request) = reply.parse::<CalloutShowRequestMessage>() {
                answer_show(state, request, tx);
            }
            true
        }
        _ => false,
    }
}

fn result(
    request_id: String,
    on_screen: Vec<String>,
    missing: Vec<String>,
) -> CalloutResultMessage {
    CalloutResultMessage {
        request_id,
        on_screen,
        missing: (!missing.is_empty()).then_some(missing),
        ..CalloutResultMessage::new()
    }
}

fn send(state: &State, result: CalloutResultMessage) {
    let _ = state.daemon.send(&result);
}

fn showing(state: &State, profile: &str) -> bool {
    profile == state.facts.profiles.selected
}

/// Reads which fixed anchors the last frame showed, then runs `then` with them.
fn on_screen(tx: &mut Tx, then: impl FnOnce(&mut State, Vec<String>, &mut Tx) + 'static) {
    let keys: Vec<(usize, String)> = FIXED
        .iter()
        .enumerate()
        .flat_map(|(i, anchor)| fixed_keys(anchor).iter().map(move |k| (i, (*k).to_owned())))
        .collect();
    let (owners, keys): (Vec<usize>, Vec<String>) = keys.into_iter().unzip();
    tx.find_elements(keys, move |state: &mut State, found, tx| {
        let mut shown = [false; FIXED.len()];
        for (owner, info) in owners.into_iter().zip(&found) {
            shown[owner] |= shows(info);
        }
        let anchors = FIXED
            .iter()
            .zip(shown)
            .filter(|(_, shown)| *shown)
            .map(|(anchor, _)| (*anchor).to_owned())
            .collect();
        then(state, anchors, tx);
    });
}

fn answer_anchors(state: &mut State, request: CalloutAnchorsRequestMessage, tx: &mut Tx) {
    if !showing(state, &request.profile_id) {
        send(state, result(request.request_id, vec![], vec![]));
        return;
    }
    on_screen(tx, move |state, anchors, _| {
        send(state, result(request.request_id, anchors, vec![]));
    });
}

fn answer_show(state: &mut State, request: CalloutShowRequestMessage, tx: &mut Tx) {
    let mut anchors: Vec<String> = Vec::new();
    for step in &request.steps {
        if !anchors.contains(&step.anchor) {
            anchors.push(step.anchor.clone());
        }
    }
    if !showing(state, &request.profile_id) || covered(state) || request.steps.is_empty() {
        send(state, result(request.request_id, vec![], anchors));
        return;
    }
    on_screen(tx, move |state, shown, tx| {
        let request_id = request.request_id.clone();
        let missing: Vec<String> = anchors
            .into_iter()
            .filter(|a| is_fixed(a) && !shown.contains(a))
            .collect();
        if missing.is_empty() {
            show(state, request, tx);
        }
        send(state, result(request_id, shown, missing));
    });
}

fn show(state: &mut State, request: CalloutShowRequestMessage, tx: &mut Tx) {
    let callout = Callout {
        profile: request.profile_id,
        from: request.from,
        steps: request.steps,
        index: 0,
        hidden: false,
        released: false,
    };
    state.window.callout = Some(callout);
    reveal(state, tx);
    take_keys(state, tx);
}

/// Another layer holds the window, so a callout would sit under it.
fn covered(state: &State) -> bool {
    state.window.layer.is_some()
}

/// Brings the step's agent or desktop row into the sidebar's built rows: the queue's agent list
/// opens when the agent is not in the queue, and the list scrolls to the row.
fn reveal(state: &mut State, tx: &mut Tx) {
    let Some(callout) = &state.window.callout else {
        return;
    };
    let step = callout.step().clone();
    if !matches!(step.anchor.as_str(), "agent" | "desktop") {
        return;
    }
    if crate::sidebar::queue_flow(state) {
        if step.anchor == "agent"
            && !step_keys(state, &step)
                .iter()
                .any(|k| k.starts_with("queue-"))
        {
            state.window.agent_list_open = true;
        }
        if let Some(key) = step_keys(state, &step)
            .into_iter()
            .find(|k| k.starts_with("queue-"))
        {
            tx.scroll_into_view_in(crate::sidebar::QUEUE_LIST, key);
        }
    } else if let Some(key) = step_keys(state, &step).pop() {
        tx.scroll_into_view_in(crate::sidebar::DESKTOP_LIST, key);
    }
}

fn advance(state: &mut State, tx: &mut Tx) {
    let Some(callout) = &mut state.window.callout else {
        return;
    };
    if !callout.last() {
        callout.index += 1;
        reveal(state, tx);
        take_keys(state, tx);
        return;
    }
    let step = callout.step().clone();
    state.window.callout = None;
    if let Some(session) = step.session_id.filter(|_| step.anchor == "agent") {
        crate::sidebar::open_agent(state, &session, tx);
    } else if let Some(desktop) = step.desktop_id.filter(|_| step.anchor == "desktop") {
        crate::dispatch::run(state, Action::GoToDesktop { desktop }, tx);
    } else {
        tx.blur();
    }
}

fn end(state: &mut State, tx: &mut Tx) {
    if state.window.callout.take().is_some() {
        tx.blur();
    }
}

/// A press elsewhere ends a one-step callout; a tour stays and gives the keys back to the
/// window. The press then acts where it was aimed.
fn pressed_outside(state: &mut State, tx: &mut Tx) {
    let Some(callout) = &mut state.window.callout else {
        return;
    };
    if callout.tour() {
        callout.released = true;
        tx.blur();
    } else {
        end(state, tx);
    }
}

/// The card has the keyboard again: shown, stepped or pressed.
fn take_keys(state: &mut State, tx: &mut Tx) {
    if let Some(callout) = &mut state.window.callout {
        callout.released = false;
        tx.focus(CARD, false);
    }
}

fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let released = state.window.callout.as_ref().is_none_or(|c| c.released);
    if released || ev.mods != Mods::default() {
        return false;
    }
    match ev.key {
        Key::Enter | Key::Right => advance(state, tx),
        Key::Escape => end(state, tx),
        _ => return false,
    }
    true
}

/// Follows what moved since the last frame: another layer opening or closing over the
/// window, and the window switching to another profile.
pub(crate) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    let covered = covered(state);
    let selected = state.facts.profiles.selected.clone();
    let Some(callout) = &mut state.window.callout else {
        return false;
    };
    if callout.profile != selected || (covered && !callout.tour()) {
        state.window.callout = None;
        return true;
    }
    if covered == callout.hidden {
        return false;
    }
    callout.hidden = covered;
    if !covered && !callout.released {
        tx.focus(CARD, false);
    }
    true
}

/// The ring around the step's target and the card beside it, over everything else.
pub(crate) fn layers(state: &State, cx: &Cx<State>, t: &Tokens) -> Vec<El> {
    let Some(callout) = state.window.callout.as_ref().filter(|c| !c.hidden) else {
        return vec![];
    };
    let step = callout.step();
    let target = step_keys(state, step).into_iter().find_map(|key| {
        cx.window_layout(&key)
            .filter(|r| r[2] > 0.0 && r[3] > 0.0)
            .map(|r| (key, r))
    });
    let card = card(callout, target.is_none(), t);
    let Some((key, rect)) = target else {
        return vec![centered(card)];
    };
    let side = if key.starts_with("queue-bar") || key.starts_with("titlebar.") {
        Side::Bottom
    } else {
        Side::Right
    };
    vec![ring(rect, t), card.anchored(key, side, Align::Start, GAP)]
}

fn ring(rect: [f32; 4], t: &Tokens) -> El {
    el().key("callout.ring")
        .absolute(rect[0] - RING_OUTSET, rect[1] - RING_OUTSET)
        .size(rect[2] + RING_OUTSET * 2.0, rect[3] + RING_OUTSET * 2.0)
        .border(2.0, t.accent)
        .radius(9.0)
        .a11y_hidden()
        .inert()
}

fn centered(card: El) -> El {
    el().key("callout.center")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            col()
                .w_full()
                .h_full()
                .items_center()
                .justify_center()
                .child(card),
        )
}

fn card(callout: &Callout, unanchored: bool, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let step = callout.step();
    let action = match (
        callout.last(),
        step.session_id.is_some() || step.desktop_id.is_some(),
    ) {
        (false, _) => "Next",
        (true, true) => "Go there",
        (true, false) => "Done",
    };
    let small = |s: String, color| text(s).font(type_::LABEL).color(color);
    let heading = row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(
            small(callout.from.clone(), t.text_secondary)
                .key("callout.from")
                .grow(1.0),
        )
        .child(
            small(
                format!("{} of {}", callout.index + 1, callout.steps.len()),
                t.text_secondary,
            )
            .key("callout.count"),
        )
        .child(
            GlyphButton::new(icon::CLOSE, "End callout")
                .help("End callout (Esc)")
                .on_press_tx(end)
                .build(k)
                .key("callout.end"),
        );
    let missing = unanchored.then(|| {
        let label = step.label.clone().unwrap_or_else(|| step.anchor.clone());
        small(format!("{label} isn't on screen"), t.text_secondary).key("callout.unanchored")
    });
    let actions = row()
        .w_full()
        .items_center()
        .gap(space::S3)
        .child(
            Button::new(format!("↵ {action}"))
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .on_press_tx(advance)
                .build(k)
                .key("callout.next"),
        )
        .child(el().grow(1.0))
        .child(small("esc end".into(), t.text_secondary).a11y_hidden());
    let body = col()
        .key(CARD)
        .role("dialog")
        .label(format!("Callout from {}", callout.from))
        .focusable(true)
        .no_focus_ring()
        .on_key(on_key)
        .on_press_tx(take_keys)
        .on_press_outside(pressed_outside)
        .w(CARD_WIDTH)
        .padding(space::S3)
        .gap(space::S2)
        .child(heading)
        .children(missing)
        .child(
            text(step.text.clone())
                .key("callout.text")
                .font(type_::BODY)
                .color(t.text_primary)
                .wrap_anywhere(),
        )
        .child(actions);
    rune_kit::popover::raised(k, body, 10.0).border(1.0, t.accent)
}
