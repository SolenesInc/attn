//! Crew members between sessions as sidebar rows: a start that asks for a second click before
//! it wakes the member, and the member's menu.

use attn_protocol::CrewMember;
use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens};
use rune::{El, Tx, el, row, text};
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
    /// The Chief, whose start takes one click.
    pub(crate) chief: bool,
    /// The harness it starts on, while the sidebar shows harness logos.
    pub(crate) harness: Option<String>,
}

pub(crate) fn resting(state: &State, member: &str) -> Resting {
    let found = crate::crew::members(state)
        .into_iter()
        .find(|m| m.id == member);
    let name = found
        .map(|m| m.name.clone())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| crate::asks::crew_display_name(member));
    let harness =
        super::logos(state).then(|| found.map(|m| m.resolved_agent.clone()).unwrap_or_default());
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
        chief: crate::chief::member(state).is_some_and(|c| c.id == member),
        harness,
    }
}

/// The lead of a crew row with no session: its harness's logo, or the dot when the sidebar
/// shows no logos, in the idle colour, as the Tauri app's `SessionLead` in state idle.
fn idle_lead(harness: Option<&str>, t: &Tokens) -> El {
    let lead = match harness {
        Some(harness) => rune::icon(super::harness_icon::of(harness), 14.0),
        None => el().size(8.0, 8.0).radius(4.0).fill(t.state_idle),
    };
    row()
        .w(14.0)
        .justify_center()
        .child(lead.color(t.state_idle))
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
    let one_click = crate::chief::member(state).is_some_and(|c| c.id == member);
    let armed = match &state.window.crew_rows.arm {
        Some(arm) if arm.member == member => arm.phase,
        _ if one_click => Phase::Armed,
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
    if one_click {
        crate::chief::start(state, tx);
    } else {
        crate::crew::start(state, member, tx);
    }
}

/// Stands an armed start down; whether one was armed.
pub(crate) fn disarm(state: &mut State) -> bool {
    let armed = state
        .window
        .crew_rows
        .arm
        .as_ref()
        .is_some_and(|arm| arm.phase == Phase::Armed);
    if armed {
        state.window.crew_rows.arm = None;
    }
    armed
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

const SUN_SIDE: f32 = 20.0;

pub(crate) const QUEUE_ROW: &str = "queue-crew.member";
pub(crate) const CLASSIC_ROW: &str = "sidebar.crew";

/// A member between sessions: pressing the row or its sun starts the member in two clicks.
pub(crate) fn row_view(key: String, m: &Resting, t: &Tokens) -> El {
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
        let sun = GlyphButton::new(&attn_ui::icon::CREW_SUN, start_label.clone())
            .icon_size(14.0)
            .side(SUN_SIDE)
            .variant(sun_variant);
        let sun = match m.phase {
            None => sun.color(t.text_dimmed),
            Some(_) => sun,
        };
        sun.on_press_tx(move |state: &mut State, tx: &mut Tx| trigger(state, &row_key, &member, tx))
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
    let mut row = super::leaf_row(key.clone(), false, t)
        .role("listitem")
        .label(start_label)
        .a11y_value(if armed { "armed" } else { "between sessions" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| trigger(state, &pressed, &member, tx))
        .child(idle_lead(m.harness.as_deref(), t))
        .child(
            text(m.name.clone())
                .key(format!("{key}.name"))
                .font(type_::BODY)
                .color(if m.chief {
                    t.text_primary
                } else {
                    t.text_tertiary
                })
                .single_line()
                .truncate(),
        )
        .child(
            row()
                .grow(1.0)
                .min_w(0.0)
                .justify_end()
                .items_center()
                .gap(7.0)
                .children(m.chief.then(|| {
                    rune::icon(&attn_ui::icon::CHIEF, 12.0)
                        .key(format!("{key}.chief"))
                        .color(t.text_tertiary)
                        .help(m.name.clone())
                }))
                .children(m.chief.then(|| {
                    text("between sessions")
                        .key(format!("{key}.age"))
                        .font(type_::MONO_CAPTION)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate()
                })),
        )
        .child(sun)
        .hover_group();
    let controls = rune::row()
        .key(format!("{key}.controls"))
        .pin(None, Some(0.0), Some(SUN_SIDE + 14.0), Some(0.0))
        .items_center()
        .gap(2.0)
        .pl(6.0)
        .fill(t.bg_elevated)
        .radius(4.0)
        .children(armed.then(|| {
            text("confirm")
                .key(format!("{key}.confirm"))
                .font(type_::LABEL)
                .color(t.warning)
        }))
        .child(actions);
    row = row.child(if armed {
        controls
    } else {
        controls.group_opacity(0.0, 1.0)
    });
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

/// The Chief's slot before it has a session: Set up opens its setup, and the slot says when
/// the Chief is starting.
pub(crate) fn chief_setup_row(
    key: String,
    chief: &crate::chief::View,
    logos: bool,
    t: &Tokens,
) -> El {
    use crate::chief::Phase;
    let pill = if chief.phase == Phase::Unconfigured {
        "Set up ⏎"
    } else {
        "starting…"
    };
    let phase = match chief.phase {
        Phase::Unconfigured => "unconfigured",
        Phase::Configuring => "configuring",
        _ => "starting",
    };
    let mut row = super::leaf_row(key.clone(), false, t)
        .role("button")
        .label(format!("Set up {}", chief.name))
        .a11y_value(phase)
        .hover(rune::Color::TRANSPARENT)
        .child(idle_lead(
            logos.then_some(chief.agent.as_deref().unwrap_or_default()),
            t,
        ))
        .child(
            text(chief.name.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0),
        )
        .child(
            text(pill)
                .key(format!("{key}.pill"))
                .font(rune::TextStyle {
                    weight: 600.0,
                    ..type_::LABEL
                })
                .color(t.brand)
                .fill(t.brand.alpha(0.14))
                .radius(999.0)
                .px(7.0)
                .py(1.0),
        );
    if chief.busy {
        row = row.disabled(true);
    } else {
        row = row
            .focusable(true)
            .hover(t.bg_elevated)
            .on_press_tx(crate::chief::edit)
            .on_key(|state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                if ev.key == rune::Key::Enter {
                    crate::chief::edit(state, tx);
                    return true;
                }
                false
            });
    }
    row
}

fn open_menu(state: &mut State, member: &str, under: &str, tx: &mut Tx) {
    state.window.crew_rows.menu = Some(Menu {
        member: member.to_owned(),
        under: under.to_owned(),
    });
    let chief = crate::chief::member(state).is_some_and(|c| c.id == member);
    tx.focus(
        format!("{MENU}.{}", if chief { "route" } else { "details" }),
        true,
    );
}

/// Opens Manage crew on `member`.
pub(crate) fn details(state: &mut State, member: &str, opener: Option<String>, tx: &mut Tx) {
    crate::crew::open(state, Some(member.to_owned()), tx);
    state.window.crew.opener = opener;
}

/// Opens Manage crew on `member`'s charter.
pub(crate) fn charter(state: &mut State, member: &str, opener: Option<String>, tx: &mut Tx) {
    crate::crew::open_on(
        state,
        Some(member.to_owned()),
        crate::crew::Tab::Charter,
        tx,
    );
    state.window.crew.opener = opener;
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
    let chief = crate::chief::view(state).filter(|c| c.key == menu.member);
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
    let is_chief = chief.is_some();
    if is_chief {
        kit_menu = kit_menu.item(
            MenuItem::new("Harness and model…")
                .key(format!("{MENU}.route"))
                .on_select_tx(|state: &mut State, tx: &mut Tx| {
                    state.window.crew_rows.menu = None;
                    crate::chief::edit(state, tx);
                }),
        );
    }
    kit_menu = kit_menu.item(
        MenuItem::new(if is_chief {
            "Edit charter"
        } else {
            "Member details"
        })
        .key(format!("{MENU}.details"))
        .lead(
            row()
                .w(14.0)
                .justify_center()
                .child(rune::icon(&attn_ui::icon::CHIEF, 12.0).color(t.text_tertiary)),
        )
        .on_select_tx(move |state: &mut State, tx: &mut Tx| {
            let opener = state.window.crew_rows.menu.take().map(|m| m.under);
            if is_chief {
                charter(state, &id, opener, tx);
            } else {
                details(state, &id, opener, tx);
            }
        }),
    );
    if let Some(chief) = chief.filter(|c| c.agent.is_some()) {
        kit_menu = kit_menu.item(
            MenuItem::new(format!("Start {}", chief.name))
                .key(format!("{MENU}.start"))
                .on_select_tx(|state: &mut State, tx: &mut Tx| {
                    state.window.crew_rows.menu = None;
                    crate::chief::start(state, tx);
                }),
        );
    }
    let mut layer = Vec::new();
    kit_menu.build(attn_ui::kit(t), &mut layer);
    layer
}
