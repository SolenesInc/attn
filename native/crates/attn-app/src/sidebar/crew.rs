//! Crew members between sessions as sidebar rows: a start that asks for a second click before
//! it wakes the member, and the member's menu.

use attn_protocol::CrewMember;
use attn_ui::Tokens;
use attn_ui::blocks::Inline;
use attn_ui::system::{self, IconSize, ListRow, Reveal, Type, words};
use rune::{El, Tx};
use rune_kit::{MenuItem, Placement};

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

fn idle_lead(harness: Option<&str>, t: &Tokens) -> El {
    match harness {
        Some(harness) => system::icon(
            super::harness_icon::of(harness),
            IconSize::Regular,
            t.state_idle,
        ),
        None => system::dot(super::MARK).fill(t.state_idle),
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
    let (member, pressed, outside) = (m.member.clone(), key.clone(), m.member.clone());
    let sun = {
        let (member, row_key) = (m.member.clone(), key.clone());
        let sun = system::icon_button(
            format!("{key}.start"),
            start_label.clone(),
            &attn_ui::icon::CREW_SUN,
            IconSize::Small,
        );
        let sun = match m.phase {
            None => sun.ink(t.text_dimmed),
            Some(_) => sun.ink(t.warning).expanded(true),
        };
        El::from(
            sun.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                trigger(state, &row_key, &member, tx)
            })
            .build(t),
        )
    };
    let actions = {
        let (member, under) = (m.member.clone(), format!("{key}.actions"));
        super::queue_view::control(
            &key,
            "actions",
            format!("Actions for {}", m.name),
            "•••",
            move |state, tx| open_menu(state, &member, &under, tx),
        )
    };
    let ink = if m.chief {
        t.text_primary
    } else {
        t.text_tertiary
    };
    let name = words(m.name.clone(), Type::Body, ink)
        .key(format!("{key}.name"))
        .single_line();
    let between = m.chief.then(|| {
        Inline::joined()
            .grow(1.0)
            .justify_end()
            .child(
                system::icon(&attn_ui::icon::CHIEF, IconSize::Small, t.text_tertiary)
                    .key(format!("{key}.chief"))
                    .help(m.name.clone()),
            )
            .child(
                words("between sessions", Type::Mono, t.text_tertiary)
                    .key(format!("{key}.age"))
                    .single_line()
                    .truncate()
                    .shrink(),
            )
    });
    let row = ListRow::new(key.clone(), start_label)
        .role("listitem")
        .unfocusable()
        .lead(idle_lead(m.harness.as_deref(), t))
        .title_el(
            Inline::joined()
                .grow(1.0)
                .child(if m.chief {
                    name.truncate()
                } else {
                    name.truncate().shrink()
                })
                .children(between),
        );
    let row = if armed {
        let confirm = words("confirm", Type::Label, t.warning).key(format!("{key}.confirm"));
        row.controls([confirm, actions.build(t).into(), sun], Reveal::Always)
    } else {
        row.hover_controls([actions])
            .controls([sun], Reveal::Always)
    };
    let mut row = row
        .build(t)
        .a11y_value(if armed { "armed" } else { "between sessions" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| trigger(state, &pressed, &member, tx));
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
    let row = ListRow::new(key.clone(), format!("Set up {}", chief.name))
        .lead(idle_lead(
            logos.then_some(chief.agent.as_deref().unwrap_or_default()),
            t,
        ))
        .title(chief.name.clone())
        .aside(words(pill, Type::Label, t.brand).key(format!("{key}.pill")))
        .disabled(chief.busy)
        .build(t)
        .a11y_value(phase);
    if chief.busy {
        return row;
    }
    row.on_press_tx(crate::chief::edit).on_key(
        |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            if ev.key == rune::Key::Enter {
                crate::chief::edit(state, tx);
                return true;
            }
            false
        },
    )
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
        .lead(system::icon(
            &attn_ui::icon::CHIEF,
            IconSize::Small,
            t.text_tertiary,
        ))
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
