//! What's new: a short tour of the release's ideas, one step at a time, shown on the first
//! launch of a release, replayable from the command palette and from Home's banner.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Mods, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog};

use crate::State;
use crate::action::Action;
use crate::keymap::Keymap;
use crate::shell::Layer;

mod scenes;
use scenes::Scene;

/// The story this release tells. A new story gets a new id, so everyone sees it once.
pub const RELEASE: &str = "profiles-desktops-intro-2026-10";
/// The release whose tour the user finished or closed.
pub const SEEN_PREF: &str = "attn.whats_new.last_seen";
/// The release whose Home banner the user dismissed.
pub const BANNER_PREF: &str = "attn.whats_new.banner_dismissed";

/// App settings that live in memory, for someone who already met this release's tour and
/// dismissed its Home banner.
pub fn seen_prefs() -> rune::prefs::Prefs {
    let mut prefs = rune::prefs::Prefs::memory();
    for key in [SEEN_PREF, BANNER_PREF] {
        prefs
            .set(key, &RELEASE.to_owned())
            .expect("memory prefs keep every value");
    }
    prefs
}

const DIALOG: &str = "whats-new";
const PRIMARY: &str = "whats-new.primary";

/// What the tour remembers between frames.
#[derive(Default)]
pub(crate) struct Tour {
    pub(crate) step: usize,
    greeted: bool,
    banner_dismissed: bool,
}

struct Step {
    title: &'static str,
    body: &'static str,
    keys: Vec<(&'static str, String)>,
    scene: Scene,
    press: Option<String>,
}

/// The tour's steps, with keys as the user's bindings draw them.
fn steps(keymap: &Keymap) -> Vec<Step> {
    let label = |action: Action| keymap.binding(&action).map(|b| keymap.label(b));
    let digits =
        |action: Action| label(action).map(|l| format!("{}1–9", l.strip_suffix('1').unwrap_or(&l)));
    let keys = |entries: Vec<(&'static str, Option<String>)>| {
        entries
            .into_iter()
            .filter_map(|(name, keys)| Some((name, keys?)))
            .collect()
    };
    let either = |a: Action, b: Action| match (label(a), label(b)) {
        (Some(a), Some(b)) => Some(format!("{a}/{b}")),
        (a, b) => a.or(b),
    };
    vec![
        Step {
            title: "One profile per world",
            body: "Work, personal, that side project: each gets its own agents, crew, automations and desktops, so only the one you're focused on is in front of you. The other profiles' agents disappear from view, so there are no work agents to tune out on a personal evening. Switch freely without interrupting the agents.",
            keys: keys(vec![("Switch profile", label(Action::ProfileSwitch))]),
            scene: Scene::Profiles,
            press: label(Action::ProfileSwitch),
        },
        Step {
            title: "Desktops arrange your agents",
            body: "Group related agents on a desktop and flip between desktops with a keystroke. The sidebar keeps listing every agent in the profile, but only the current desktop's agents are on screen.",
            keys: keys(vec![
                ("Switch desktop", digits(Action::DesktopSelect1)),
                ("See all desktops", label(Action::DesktopOverview)),
            ]),
            scene: Scene::Desktops,
            press: label(Action::DesktopSelect2),
        },
        Step {
            title: "Arrange each desktop your way",
            body: "Drag panes by their header to rearrange them, open a shell beside an agent, or give one pane the whole desktop when you need room. When an agent belongs somewhere else, send it to another desktop and go with it, or leave it there and stay where you are.",
            keys: keys(vec![
                ("Focus one pane", label(Action::TerminalToggleMaximize)),
                ("Move and follow", digits(Action::DesktopSend1)),
                ("Move and stay", digits(Action::DesktopSendStay1)),
            ]),
            scene: Scene::Arrange,
            press: label(Action::DesktopSend2),
        },
        Step {
            title: "The queue decides what's next",
            body: "Turn on the agent queue from the command palette and your agents split into waiting for you or busy. When you're done with one, settle it and attn moves you to the next in the queue. You never have to choose which agent to look at next, so you tire less and get more work done.",
            keys: keys(vec![
                ("Settle", label(Action::SessionSettle)),
                (
                    "Step through the queue",
                    either(Action::SessionPrev, Action::SessionNext),
                ),
                ("Next waiting agent", label(Action::SessionJumpToWaiting)),
                ("Next automation run", label(Action::SessionNextRun)),
            ]),
            scene: Scene::Queue,
            press: label(Action::SessionSettle),
        },
        Step {
            title: "Hide the sidebar, keep the queue",
            body: "Since the queue moves you from agent to agent, you rarely need the sidebar. Hide it and a slim bar across the top keeps who's waiting, your automation runs and your desktops, while the rest of the window goes to the agent in front of you.",
            keys: keys(vec![
                (
                    "Show or hide the sidebar",
                    label(Action::SessionToggleSidebar),
                ),
                ("All agents", label(Action::SidebarAgentList)),
            ]),
            scene: Scene::QueueBar,
            press: label(Action::SessionToggleSidebar),
        },
        Step {
            title: "Jump to any agent",
            body: "Open the agent palette and type a few letters to jump to any agent, tile or crew member, on any desktop. From the list you can settle or snooze an agent without opening it, or type > to switch to commands.",
            keys: keys(vec![("Agent palette", label(Action::UiActionMenu))]),
            scene: Scene::AgentPalette,
            press: label(Action::UiActionMenu),
        },
        Step {
            title: "Keep your hands on the keyboard",
            body: "Most of what attn can do is in the command palette: make a new desktop, switch profile, turn on the agent queue or auto-settle, open the notebook or the Garden. Commands that have a shortcut show it, so the palette is also where you pick them up.",
            keys: keys(vec![("Command palette", label(Action::UiCommandPalette))]),
            scene: Scene::CommandPalette,
            press: label(Action::UiCommandPalette),
        },
    ]
}

fn step_count(state: &State) -> usize {
    steps(&state.keymap).len()
}

/// Opens the tour on the first launch of a release, once per window's life, once the window
/// reaches the daemon. A tile window never greets.
pub(crate) fn greet(state: &mut State, tx: &mut Tx) -> bool {
    let tour = &mut state.window.whats_new;
    if tour.greeted
        || state.window.tile.is_some()
        || state.facts.connection != attn_client::Connection::Connected
    {
        return false;
    }
    tour.greeted = true;
    tour.banner_dismissed = crate::frame::pref_string(BANNER_PREF).as_deref() == Some(RELEASE);
    if crate::frame::pref_string(SEEN_PREF).as_deref() == Some(RELEASE) {
        return true;
    }
    open(state, tx);
    true
}

/// Opens the tour on its first step.
pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    state.window.whats_new.step = 0;
    state.window.layer = Some(Layer::WhatsNew);
    tx.focus(PRIMARY, false);
}

/// Closes the tour and remembers this release as seen.
pub(crate) fn dismiss(state: &mut State, tx: &mut Tx) {
    if state.window.layer == Some(Layer::WhatsNew) {
        state.window.layer = None;
        tx.blur();
    }
    crate::frame::set_pref_string(SEEN_PREF, RELEASE);
}

fn go(state: &mut State, step: isize, tx: &mut Tx) {
    let last = step_count(state) - 1;
    let tour = &mut state.window.whats_new;
    tour.step = step.clamp(0, last as isize) as usize;
    if tour.step == 0 {
        tx.focus(PRIMARY, false);
    }
}

fn advance(state: &mut State, tx: &mut Tx) {
    let step = state.window.whats_new.step;
    if step + 1 == step_count(state) {
        dismiss(state, tx);
    } else {
        go(state, step as isize + 1, tx);
    }
}

fn back(state: &mut State, tx: &mut Tx) {
    go(state, state.window.whats_new.step as isize - 1, tx);
}

fn view_shortcuts(state: &mut State, tx: &mut Tx) {
    dismiss(state, tx);
    state.window.layer = Some(Layer::Shortcuts);
}

fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.mods != Mods::default() {
        return false;
    }
    match ev.key {
        Key::Right => advance_only(state, tx),
        Key::Left => back(state, tx),
        _ => return false,
    }
    true
}

/// The right arrow steps forward and stops on the last step; only Got it closes the tour.
fn advance_only(state: &mut State, tx: &mut Tx) {
    go(state, state.window.whats_new.step as isize + 1, tx);
}

impl Tour {
    pub(crate) fn banner_dismissed(&self) -> bool {
        self.banner_dismissed
    }
}

/// Home's banner offering the tour again, until the user dismisses it for this release.
pub(crate) fn banner(state: &State, t: &Tokens) -> Option<El> {
    if state.window.whats_new.banner_dismissed {
        return None;
    }
    let k = attn_ui::kit(t);
    Some(
        row()
            .key("home.intro-banner")
            .role("group")
            .label("New in attn")
            .w_full()
            .items_center()
            .gap(space::S3)
            .padding(space::S2)
            .pl(space::S6)
            .radius(6.0)
            .fill(t.brand.alpha(0.06))
            .border(1.0, t.brand.alpha(0.22))
            .child(
                row()
                    .grow(1.0)
                    .min_w(0.0)
                    .gap(space::S1)
                    .child(text("New in attn:").font(type_::SECTION).color(t.brand))
                    .child(
                        text("profiles, desktops, the agent queue and two palettes.")
                            .font(type_::BODY)
                            .color(t.text_secondary)
                            .wrap(),
                    ),
            )
            .child(
                Button::new("Replay the intro")
                    .variant(ButtonVariant::Submit)
                    .control_size(ControlSize::Small)
                    .on_press_tx(open)
                    .build(k)
                    .key("home.intro-banner.replay"),
            )
            .child(
                attn_ui::GlyphButton::new(attn_ui::icon::CLOSE, "Dismiss the intro banner")
                    .help("Dismiss the intro banner")
                    .on_press(|state: &mut State| {
                        state.window.whats_new.banner_dismissed = true;
                        crate::frame::set_pref_string(BANNER_PREF, RELEASE);
                    })
                    .build(k)
                    .key("home.intro-banner.dismiss"),
            ),
    )
}

/// The tour's dialog, while it is open.
pub(crate) fn dialog(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if state.window.layer != Some(Layer::WhatsNew) {
        return;
    }
    let k = attn_ui::kit(t);
    let steps = steps(&state.keymap);
    let index = state.window.whats_new.step.min(steps.len() - 1);
    let step = &steps[index];
    let last = index + 1 == steps.len();
    let keys = row()
        .key("whats-new.keys")
        .role("list")
        .label("Keys")
        .w_full()
        .flex_wrap()
        .gap(space::S3)
        .children(step.keys.iter().map(|(name, keys)| {
            row()
                .key(format!("whats-new.key.{name}"))
                .role("listitem")
                .label(format!("{keys} {name}"))
                .items_center()
                .gap(space::S2)
                .child(
                    text(keys.clone())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_primary)
                        .a11y_hidden(),
                )
                .child(
                    text(*name)
                        .font(type_::BODY)
                        .color(t.text_secondary)
                        .a11y_hidden(),
                )
        }));
    let dots = row()
        .key("whats-new.dots")
        .gap(space::S1)
        .items_center()
        .children(steps.iter().enumerate().map(|(i, s)| {
            let current = i == index;
            rune::el()
                .key(format!("whats-new.dot.{i}"))
                .role("button")
                .label(format!("Step {}: {}", i + 1, s.title))
                .a11y_value(if current { "current" } else { "" })
                .size(7.0, 7.0)
                .radius(3.5)
                .fill(if current { t.brand } else { t.border })
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    go(state, i as isize, tx);
                    tx.focus(PRIMARY, false);
                })
        }));
    let footer = row()
        .key("whats-new.footer")
        .w_full()
        .items_center()
        .gap(space::S3)
        .child(
            Button::new("View all shortcuts →")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .on_press_tx(view_shortcuts)
                .build(k)
                .key("whats-new.shortcuts"),
        )
        .child(rune::el().grow(1.0))
        .child(dots)
        .child(rune::el().grow(1.0))
        .children((index > 0).then(|| {
            Button::new("Back")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .on_press_tx(back)
                .build(k)
                .key("whats-new.back")
        }))
        .child(
            Button::new(if last { "Got it" } else { "Next" })
                .variant(ButtonVariant::Submit)
                .control_size(ControlSize::Small)
                .on_press_tx(advance)
                .build(k)
                .key(PRIMARY)
                .default_button(),
        );
    let body = col()
        .key("whats-new.step")
        .w_full()
        .gap(space::S3)
        .padding(space::S6)
        .pt(0.0)
        .on_key(on_key)
        .child(scenes::scene(step.scene, step.press.clone(), t))
        .child(
            text(step.title)
                .key("whats-new.title")
                .role("heading")
                .level(2)
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(
            text(step.body)
                .key("whats-new.body")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
        .children((!step.keys.is_empty()).then_some(keys))
        .child(footer);
    Dialog::new(DIALOG, "What's new")
        .open(true)
        .fit()
        .width(scenes::WIDTH + 2.0 * space::S6)
        .click_outside(true)
        .header(
            text(format!("{} of {}", index + 1, steps.len()))
                .key("whats-new.count")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
        .on_close_tx(dismiss)
        .child(body)
        .build(k, layers);
}
