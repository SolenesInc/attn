//! Crew members between sessions as sidebar rows: a start that asks for a second click before
//! it wakes the member, and the member's menu.

use attn_protocol::CrewMember;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, Tx, row, text};
use rune_kit::{ButtonVariant, MenuItem, Placement};

use crate::State;

/// How long an armed start waits for its second click: a tripwire about ten times the few
/// hundred milliseconds a confirming click takes.
pub(crate) const ARM_SECONDS: f64 = 4.0;

/// How long a confirmed start shows its flare, during which further clicks do nothing.
const BREAK_SECONDS: f64 = 0.62;

const MENU: &str = "crew-member-menu";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Phase {
    Armed,
    Breaking,
}

struct Arm {
    member: String,
    phase: Phase,
    until: f64,
    focus: Option<String>,
}

struct Menu {
    member: String,
    under: String,
}

/// The sidebar's crew rows: the one start armed, and the member menu open.
#[derive(Default)]
pub(crate) struct Rows {
    arm: Option<Arm>,
    menu: Option<Menu>,
}

impl Rows {
    /// The member whose start is armed or breaking, and which.
    pub(crate) fn armed(&self) -> Option<(String, Phase)> {
        self.arm.as_ref().map(|arm| (arm.member.clone(), arm.phase))
    }
}

/// A member between sessions, as its row shows it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Resting {
    pub(crate) member: String,
    pub(crate) name: String,
    pub(crate) phase: Option<Phase>,
}

pub(crate) fn resting(state: &State, member: &str) -> Resting {
    let name = crate::crew::members(state)
        .into_iter()
        .find(|m| m.id == member)
        .map(|m| m.name.clone())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| crate::asks::crew_display_name(member));
    let phase = state
        .window
        .crew_rows
        .arm
        .as_ref()
        .filter(|arm| arm.member == member)
        .map(|arm| arm.phase);
    Resting {
        member: member.to_owned(),
        name,
        phase,
    }
}

/// The members of the selected profile with no session, in key order.
pub(crate) fn between_sessions(state: &State) -> Vec<&CrewMember> {
    let mut members: Vec<&CrewMember> = crate::crew::members(state)
        .into_iter()
        .filter(|m| m.binding_session.is_none())
        .collect();
    members.sort_by(|a, b| a.id.cmp(&b.id));
    members
}

/// A press on `member`'s start: the first arms it, the second wakes the member, and one
/// during the flare does nothing.
pub(crate) fn trigger(state: &mut State, row_key: &str, member: &str, tx: &mut Tx) {
    let now = tx.clock();
    let armed = match &state.window.crew_rows.arm {
        Some(arm) if arm.member == member => arm.phase,
        _ => {
            state.window.crew_rows.arm = Some(Arm {
                member: member.to_owned(),
                phase: Phase::Armed,
                until: now + ARM_SECONDS,
                focus: tx
                    .focused()
                    .map(str::to_owned)
                    .filter(|f| !f.starts_with(row_key)),
            });
            tx.wake_at(now + ARM_SECONDS);
            return;
        }
    };
    if armed == Phase::Breaking {
        return;
    }
    state.window.crew_rows.arm = Some(Arm {
        member: member.to_owned(),
        phase: Phase::Breaking,
        until: now + BREAK_SECONDS,
        focus: None,
    });
    tx.wake_at(now + BREAK_SECONDS);
    crate::crew::start(state, member, tx);
}

/// Stands an armed start down.
pub(crate) fn disarm(state: &mut State) {
    if state
        .window
        .crew_rows
        .arm
        .as_ref()
        .is_some_and(|arm| arm.phase == Phase::Armed)
    {
        state.window.crew_rows.arm = None;
    }
}

/// Ends an arm or a flare whose time ran out, and an arm whose focus went to another row;
/// whether one ended.
pub(crate) fn poll(state: &mut State, now: f64, row_prefixes: &[String], tx: &Tx) -> bool {
    let Some(arm) = &state.window.crew_rows.arm else {
        return false;
    };
    let focused = tx.focused();
    let wandered = arm.phase == Phase::Armed
        && focused != arm.focus.as_deref()
        && !focused.is_some_and(|f| {
            row_prefixes.iter().any(|p| {
                let row = format!("{p}{}", arm.member);
                f == row || f.starts_with(&format!("{row}."))
            })
        });
    if now >= arm.until || wandered {
        state.window.crew_rows.arm = None;
        return true;
    }
    false
}

/// The keys of the rows a member's start sits in, before the member's key.
pub(crate) fn row_prefixes() -> [String; 2] {
    [format!("{QUEUE_ROW}."), format!("{CLASSIC_ROW}.")]
}

pub(crate) const QUEUE_ROW: &str = "queue-crew.member";
pub(crate) const CLASSIC_ROW: &str = "sidebar.crew";

/// A member between sessions: pressing the row or its sun starts the member in two clicks.
pub(crate) fn row_view(key: String, m: &Resting, height: f32, t: &Tokens) -> El {
    let armed = m.phase == Some(Phase::Armed);
    let start_label = if armed {
        format!("Start {} — click again to confirm", m.name)
    } else {
        format!("Start {}", m.name)
    };
    let sun_variant = match m.phase {
        None => ButtonVariant::GhostMuted,
        Some(_) => ButtonVariant::Warning,
    };
    let (member, pressed, outside) = (m.member.clone(), key.clone(), m.member.clone());
    let sun = {
        let (member, row_key) = (m.member.clone(), key.clone());
        GlyphButton::new(attn_ui::icon::SUN, start_label.clone())
            .font(type_::LABEL)
            .side(20.0)
            .variant(sun_variant)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                trigger(state, &row_key, &member, tx)
            })
            .build(attn_ui::kit(t))
            .key(format!("{key}.start"))
            .radius(4.0)
            .hover(t.bg_button)
    };
    let actions = {
        let (member, under) = (m.member.clone(), format!("{key}.actions"));
        super::queue_view::control(
            &key,
            "actions",
            format!("Actions for {}", m.name),
            "•••",
            t,
            move |state, tx| open_menu(state, &member, &under, tx),
        )
    };
    let mut row = row()
        .key(key.clone())
        .role("listitem")
        .label(start_label)
        .a11y_value(if armed { "armed" } else { "between sessions" })
        .w_full()
        .h(height)
        .items_center()
        .gap(space::S2)
        .px(space::S1)
        .radius(4.0)
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| trigger(state, &pressed, &member, tx))
        .child(
            text(attn_ui::icon::CIRCLE_OPEN)
                .font(type_::LABEL)
                .color(t.text_muted)
                .w(14.0),
        )
        .child(
            text(m.name.clone())
                .font(type_::BODY)
                .color(t.text_secondary)
                .single_line()
                .truncate()
                .grow(1.0),
        )
        .children(armed.then(|| {
            text("confirm")
                .key(format!("{key}.confirm"))
                .font(type_::LABEL)
                .color(t.warning)
        }))
        .child(sun)
        .hover_group()
        .child(
            row()
                .key(format!("{key}.controls"))
                .group_opacity(0.0, 1.0)
                .child(actions),
        );
    if armed {
        row = row.on_press_outside(move |state: &mut State, _: &mut Tx| {
            if state
                .window
                .crew_rows
                .arm
                .as_ref()
                .is_some_and(|a| a.member == outside)
            {
                disarm(state);
            }
        });
    }
    row
}

fn open_menu(state: &mut State, member: &str, under: &str, tx: &mut Tx) {
    state.window.crew_rows.menu = Some(Menu {
        member: member.to_owned(),
        under: under.to_owned(),
    });
    tx.focus(format!("{MENU}.details"), true);
}

/// Opens Manage crew on `member`.
pub(crate) fn details(state: &mut State, member: &str, tx: &mut Tx) {
    crate::crew::open(state, Some(member.to_owned()), tx);
}

/// The member menu, over the window.
pub(crate) fn menu_view(state: &State, t: &Tokens) -> Vec<El> {
    let Some(menu) = &state.window.crew_rows.menu else {
        return Vec::new();
    };
    let member = crate::crew::members(state)
        .into_iter()
        .find(|m| m.id == menu.member);
    let name = member
        .map(|m| m.name.clone())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| crate::asks::crew_display_name(&menu.member));
    let desktop = member.and_then(|m| m.launch_desktop.as_ref()?.label.clone());
    let id = menu.member.clone();
    let mut kit_menu = rune_kit::Menu::new(MENU)
        .standard()
        .anchor(menu.under.clone())
        .placement(Placement::BottomEnd)
        .gutter(4.0)
        .min_width(200.0)
        .label(format!("Actions for {name}"))
        .open(true)
        .on_close(|state: &mut State| state.window.crew_rows.menu = None);
    if let Some(desktop) = desktop {
        kit_menu = kit_menu.group_label(desktop);
    }
    kit_menu = kit_menu.item(
        MenuItem::new("Member details")
            .key(format!("{MENU}.details"))
            .lead(
                text(attn_ui::icon::BOLT)
                    .font(type_::LABEL)
                    .color(t.text_tertiary)
                    .w(14.0),
            )
            .on_select_tx(move |state: &mut State, tx: &mut Tx| {
                state.window.crew_rows.menu = None;
                details(state, &id, tx);
            }),
    );
    let mut layer = Vec::new();
    kit_menu.build(attn_ui::kit(t), &mut layer);
    layer
}
