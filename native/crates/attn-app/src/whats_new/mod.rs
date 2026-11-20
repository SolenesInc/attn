//! What's new: a short tour of the release's ideas, one step at a time, shown on the first
//! launch of a release, replayable from the command palette and from Home's banner.

use attn_ui::blocks::{Actions, Block, DialogSize, Inline, Stack};
use attn_ui::keycap;
use attn_ui::system::{Button, Look, Size, Type, pager, words};
use attn_ui::{Dialog, Tokens, type_};
use rune::tok::space;
use rune::{El, Key, KeyEvent, Mods, Tx, col, row, text};
use rune_kit::{ButtonVariant, ControlSize};

use crate::State;
use crate::action::Action;
use crate::keymap::Keymap;
use crate::shell::Layer;

mod scenes;
use scenes::Scene;

/// The story this release tells. A new story gets a new id, so everyone sees it once.
pub const RELEASE: &str = "new-look-2026-11";
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
    keys: Vec<(&'static str, Combos)>,
    scene: Scene,
    scheme: Scheme,
}

/// A color scheme a step lets you try on the spot, and the way back to attn's own.
struct Scheme {
    id: &'static str,
    offer: &'static str,
    back: &'static str,
}

/// The keys of one entry: as a screen reader says them, and as keycaps, one group per
/// alternative.
#[derive(Clone)]
pub(super) struct Combos {
    pub said: String,
    pub caps: Vec<Vec<String>>,
}

/// The tour's steps, with keys as the user's bindings draw them.
fn steps(keymap: &Keymap) -> Vec<Step> {
    let settings = keymap.binding(&Action::UiOpenSettings).map(|b| Combos {
        said: keymap.label(b),
        caps: vec![keymap.keycaps(b)],
    });
    let keys = settings
        .clone()
        .map(|keys| vec![("Settings", keys)])
        .unwrap_or_default();
    vec![
        Step {
            title: "A new look",
            body: "attn has a new logo, a black cat watching over your agents, and new colors to go with it. Magenta marks an agent waiting for you, cyan an agent at work, and violet the main buttons. If you prefer the colors you had, switch back here, or any time in Settings → Appearance → Theme, where they are called Classic.",
            keys: keys.clone(),
            scene: Scene::NewLook,
            scheme: Scheme {
                id: "classic",
                offer: "Switch back to Classic",
                back: "Use the new colors",
            },
        },
        Step {
            title: "Night, for the full colors",
            body: "The new colors are kept quiet so they stay out of your way. Night shows them at full strength on indigo surfaces, the way the logo does. Try it here, or pick it later under Settings → Appearance → Theme.",
            keys,
            scene: Scene::Night,
            scheme: Scheme {
                id: "night",
                offer: "Try Night",
                back: "Use the default colors",
            },
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
                        text("a new look, and how to switch back.")
                            .font(type_::BODY)
                            .color(t.text_secondary)
                            .wrap(),
                    ),
            )
            .child(
                rune_kit::Button::new("Show me")
                    .variant(ButtonVariant::Submit)
                    .control_size(ControlSize::Small)
                    .on_press_tx(open)
                    .build(k)
                    .key("home.intro-banner.replay"),
            )
            .child(
                attn_ui::GlyphButton::new(attn_ui::glyph::TIMES, "Dismiss the intro banner")
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
    let steps = steps(&state.keymap);
    let index = state.window.whats_new.step.min(steps.len() - 1);
    let step = &steps[index];
    let last = index + 1 == steps.len();
    let keys = (!step.keys.is_empty()).then(|| {
        Inline::loose()
            .key("whats-new.keys")
            .role("list")
            .label("Keys")
            .children(step.keys.iter().map(|(name, keys)| {
                Inline::loose()
                    .key(format!("whats-new.key.{name}"))
                    .role("listitem")
                    .label(format!("{} {name}", keys.said))
                    .child(keycaps(keys, t).a11y_hidden())
                    .child(words(*name, Type::Body, t.text_secondary).a11y_hidden())
            }))
    });
    let titles: Vec<&str> = steps.iter().map(|s| s.title).collect();
    let dots = pager(
        ("whats-new.dots", "whats-new.dot"),
        steps.len(),
        index,
        |i| format!("Step {}: {}", i + 1, titles[i]),
        t,
        |state: &mut State, i: usize, tx: &mut Tx| {
            go(state, i as isize, tx);
            tx.focus(PRIMARY, false);
        },
    );
    let scene = Block::custom(
        "whats-new.scene.block",
        col()
            .key(format!("whats-new.step.{index}"))
            .w_full()
            .transition(step_entrance())
            .child(scenes::scene(step.scene, t)),
    );
    let told = Block::custom(
        "whats-new.told.block",
        Stack::tight()
            .w_full()
            .child(
                words(step.title, Type::Heading, t.text_primary)
                    .key("whats-new.title")
                    .role("heading")
                    .level(2)
                    .wrap(),
            )
            .child(
                words(step.body, Type::Body, t.text_secondary)
                    .key("whats-new.body")
                    .wrap(),
            ),
    );
    let scheme = Block::custom(
        "whats-new.scheme.block",
        Stack::tight()
            .child(
                El::from(
                    Button::new(scheme_label(state, &step.scheme))
                        .size(Size::Compact)
                        .on_press(scheme_press(state, &step.scheme))
                        .build(t)
                        .key("whats-new.scheme"),
                )
                .self_start(),
            )
            .children(keys),
    );
    let mut actions = Actions::new("whats-new.actions").push(
        Button::new("View all shortcuts →")
            .look(Look::Quiet)
            .on_press_tx(view_shortcuts)
            .build(t)
            .key("whats-new.shortcuts"),
    );
    actions = actions.then_center().push(dots).then_end();
    if index > 0 {
        actions = actions.push(
            Button::new("Back")
                .on_press_tx(back)
                .build(t)
                .key("whats-new.back"),
        );
    }
    actions = actions.push(
        El::from(
            Button::new(if last { "Got it" } else { "Next" })
                .look(Look::Primary)
                .on_press_tx(advance)
                .build(t)
                .key(PRIMARY),
        )
        .default_button(),
    );
    Dialog::new(DIALOG, "What's new")
        .size(DialogSize::Tour)
        .click_outside(true)
        .aside(
            words(
                format!("{} of {}", index + 1, steps.len()),
                Type::Small,
                t.text_tertiary,
            )
            .key("whats-new.count"),
        )
        .close_button()
        .on_close_tx(dismiss)
        .on_key(on_key)
        .push(scene)
        .push(told)
        .push(scheme)
        .actions(actions)
        .build(t, layers);
}

/// The scheme button's words: the step's scheme to put on, or, while it is on, attn's own back.
fn scheme_label(state: &State, scheme: &Scheme) -> &'static str {
    if crate::color_scheme(&state.facts.settings).id == scheme.id {
        scheme.back
    } else {
        scheme.offer
    }
}

fn scheme_press(state: &State, scheme: &Scheme) -> impl Fn(&mut State) + 'static {
    let id = if crate::color_scheme(&state.facts.settings).id == scheme.id {
        ""
    } else {
        scheme.id
    };
    move |state: &mut State| crate::settings::appearance::choose_color_scheme(state, id)
}

/// `@keyframes whats-new-step`: each step fades in from 8 points right over 180 ms, and the
/// step it replaces is gone at once.
fn step_entrance() -> rune::Transition {
    use rune::{Spring, SpringPair, Transition};
    Transition::asymmetric(
        Transition::offset(8.0, 0.0).combined(Transition::opacity()),
        Transition::none(),
    )
    .springs(SpringPair::new(Spring::new(0.18, 0.0), Spring::NONE))
}

pub(super) fn keycaps(keys: &Combos, t: &Tokens) -> El {
    keycap::alternatives(
        keys.caps
            .iter()
            .map(|caps| keycap::Keys::raised(caps))
            .collect(),
        t,
    )
}
