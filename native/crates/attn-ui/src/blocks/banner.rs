use rune::{Color, El, Tx, row};

use crate::system::{Button, Look, Radius, Size, Space, Type, door, focus, layers, words};
use crate::{Tokens, colors, icon};

/// What a banner is about, which decides its colour and its glyph.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum BannerTone {
    /// Something needs the person.
    Warn,
    /// Something is under way.
    Calm,
    /// Something ended well.
    Good,
    /// Something failed: the one loud banner, filled in its tone.
    Bad,
}

type Attach = Box<dyn FnOnce(El) -> El>;

enum Note {
    Words {
        key: String,
        said: String,
    },
    Link {
        key: String,
        shown: String,
        label: String,
        press: Attach,
    },
    Button {
        key: String,
        button: Button,
    },
}

/// The bar under the title bar that tells the person about something the whole window cares
/// about: a sentence, its notes, its actions right after them, and dismiss alone at the far
/// edge.
pub struct Banner {
    key: String,
    label: String,
    tone: BannerTone,
    value: Option<String>,
    sentence: Option<(String, String)>,
    notes: Vec<Note>,
    actions: Vec<(String, Button)>,
    countdown: Option<(String, u64)>,
    dismiss: Option<(String, String, Attach)>,
    hover: Option<Attach>,
}

const GLYPH: f32 = 16.0;
const CLOSE: f32 = 14.0;
/// How much of its tone a banner's fill takes over the app's background: the least share at
/// which every tone stands further from the title bar than a hover step, in dark and in light.
const TINT: f32 = 0.18;
const EDGE: f32 = 0.45;
/// WCAG's AA contrast for text below 18 points.
const READS: f32 = 4.5;

impl Banner {
    pub fn new(key: impl Into<String>, label: impl Into<String>, tone: BannerTone) -> Banner {
        Banner {
            key: key.into(),
            label: label.into(),
            tone,
            value: None,
            sentence: None,
            notes: Vec::new(),
            actions: Vec::new(),
            countdown: None,
            dismiss: None,
            hover: None,
        }
    }

    /// The state the banner reads as, for a screen reader.
    pub fn value(mut self, value: impl Into<String>) -> Self {
        self.value = Some(value.into());
        self
    }

    pub fn says(mut self, key: impl Into<String>, sentence: impl Into<String>) -> Self {
        self.sentence = Some((key.into(), sentence.into()));
        self
    }

    /// Quieter words after the sentence, ending in an ellipsis before they push an action away.
    pub fn note(mut self, key: impl Into<String>, said: impl Into<String>) -> Self {
        self.notes.push(Note::Words {
            key: key.into(),
            said: said.into(),
        });
        self
    }

    /// A name among the notes that takes the person to it, announced as `label`.
    pub fn link<S: 'static>(
        mut self,
        key: impl Into<String>,
        shown: impl Into<String>,
        label: impl Into<String>,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Self {
        self.notes.push(Note::Link {
            key: key.into(),
            shown: shown.into(),
            label: label.into(),
            press: Box::new(move |e: El| e.on_press_tx(press)),
        });
        self
    }

    /// A button among the notes, for what one of them offers.
    pub fn note_button(mut self, key: impl Into<String>, button: Button) -> Self {
        self.notes.push(Note::Button {
            key: key.into(),
            button,
        });
        self
    }

    /// What the banner offers, after its words, in the order given: the action that moves
    /// things forward first. The first action of a banner that needs the person is primary.
    pub fn action(mut self, key: impl Into<String>, button: Button) -> Self {
        self.actions.push((key.into(), button));
        self
    }

    /// Seconds until the banner closes on its own.
    pub fn countdown(mut self, key: impl Into<String>, seconds: u64) -> Self {
        self.countdown = Some((key.into(), seconds));
        self
    }

    pub fn dismiss<S: 'static>(
        mut self,
        key: impl Into<String>,
        label: impl Into<String>,
        press: impl Fn(&mut S) + 'static,
    ) -> Self {
        self.dismiss = Some((
            key.into(),
            label.into(),
            Box::new(move |e: El| e.on_press(press)),
        ));
        self
    }

    /// Hears the pointer come over the banner and leave it.
    pub fn on_hover<S: 'static>(mut self, hover: impl Fn(&mut S, bool, &mut Tx) + 'static) -> Self {
        self.hover = Some(Box::new(move |e: El| e.on_hover_change(hover)));
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let paint = Paint::of(self.tone, t);
        let glyph = row()
            .h(Size::Compact.height())
            .items_center()
            .a11y_hidden()
            .child(rune::icon(glyph(self.tone), GLYPH).color(paint.glyph));
        let sentence = self
            .sentence
            .map(|(key, said)| words(said, Type::Name, paint.ink).key(key).wrap().shrink());
        let notes = (!self.notes.is_empty()).then(|| {
            let words_only = self.notes.iter().all(|n| matches!(n, Note::Words { .. }));
            let notes = self.notes.into_iter().map(|note| paint.note(note));
            let notes = door::gap(row().items_center(), Space::S8)
                .key(format!("{}.notes", self.key))
                .grow(1.0)
                .shrink()
                .children(notes);
            if words_only {
                notes.clip_soft(0.0)
            } else {
                notes
            }
        });
        let said = door::gap(row().items_center(), Space::S8)
            .shrink()
            .children(sentence)
            .children(notes);
        let primary = self.tone == BannerTone::Warn;
        let actions = self
            .actions
            .into_iter()
            .enumerate()
            .map(|(i, (key, button))| {
                let look = if primary && i == 0 {
                    Look::Primary
                } else {
                    Look::Plain
                };
                let button = button.look(look).size(Size::Compact).build(&paint.controls);
                row().child(El::from(button.key(key)))
            });
        let countdown = self.countdown.map(|(key, seconds)| {
            words(format!("Closing in {seconds}s"), Type::Body, paint.quiet)
                .key(key)
                .single_line()
        });
        let dismiss = self
            .dismiss
            .map(|(key, label, press)| paint.dismiss(key, label, press));
        let bar = row()
            .key(self.key)
            .role("region")
            .label(self.label)
            .w_full()
            .items_center()
            .fill(paint.fill)
            .border_sides([0.0, 0.0, 1.0, 0.0], paint.edge)
            .child(glyph)
            .child(said)
            .children(actions)
            .children(countdown)
            .child(row().grow(1.0))
            .children(dismiss);
        let bar = door::inset(door::gap(bar, Space::S8), Space::S12, Space::S6);
        let bar = match self.value {
            Some(value) => bar.a11y_value(value),
            None => bar,
        };
        match self.hover {
            Some(attach) => attach(bar),
            None => bar,
        }
    }
}

fn glyph(tone: BannerTone) -> &'static rune::Icon {
    match tone {
        BannerTone::Warn => &icon::NEEDS_YOU,
        BannerTone::Calm => &icon::WORKING,
        BannerTone::Good => &icon::DONE,
        BannerTone::Bad => &icon::FAILED,
    }
}

/// The colours a banner of a tone draws with, and the tokens its controls draw with.
struct Paint {
    fill: Color,
    edge: Color,
    glyph: Color,
    ink: Color,
    quiet: Color,
    link: Color,
    controls: Tokens,
}

impl Paint {
    fn of(tone: BannerTone, t: &Tokens) -> Paint {
        let hue = match tone {
            BannerTone::Warn => t.warning,
            BannerTone::Calm => t.text_tertiary,
            BannerTone::Good => t.success,
            BannerTone::Bad => t.error,
        };
        if tone == BannerTone::Bad {
            let ink = t.bg_app;
            let fill = deepened_for(hue, ink, t.text_primary);
            return Paint {
                fill,
                edge: fill,
                glyph: ink,
                ink,
                quiet: ink,
                link: ink,
                controls: Tokens { warning: ink, ..*t },
            };
        }
        Paint {
            fill: colors::mix_srgb(t.bg_app, hue, TINT),
            edge: colors::mix_srgb(t.border, hue, EDGE),
            glyph: hue,
            ink: t.text_primary,
            quiet: t.text_secondary,
            link: t.accent,
            controls: *t,
        }
    }

    fn note(&self, note: Note) -> El {
        let t = &self.controls;
        match note {
            Note::Words { key, said } => words(said, Type::Body, self.quiet)
                .key(key)
                .truncate()
                .shrink(),
            Note::Link {
                key,
                shown,
                label,
                press,
            } => {
                let link = row()
                    .key(key)
                    .role("button")
                    .label(label)
                    .focusable(true)
                    .h(Size::Compact.height())
                    .items_center()
                    .child(words(shown, Type::Name, self.link).single_line());
                let link = door::corner(door::inset(link, Space::S4, Space::S0), Radius::Control);
                focus::ring(press(layers::pressable(link, self.fill, t)), t)
            }
            Note::Button { key, button } => {
                row().child(El::from(button.size(Size::Compact).build(t).key(key)))
            }
        }
    }

    fn dismiss(&self, key: String, label: String, press: Attach) -> El {
        let t = &self.controls;
        let side = Size::Compact.height();
        let close = row()
            .key(key)
            .role("button")
            .label(label)
            .help("Dismiss")
            .focusable(true)
            .size(side, side)
            .center()
            .child(rune::icon(&rune_kit::glyphs::CLOSE, CLOSE).color(self.quiet));
        let close = door::corner(close, Radius::Control);
        focus::ring(press(layers::pressable(close, self.fill, t)), t)
    }
}

/// `fill`, moved toward `toward` only as far as `ink` needs to read on it.
fn deepened_for(fill: Color, ink: Color, toward: Color) -> Color {
    if contrast(ink, fill) >= READS {
        return fill;
    }
    let (mut low, mut high) = (0.0f32, 1.0f32);
    for _ in 0..24 {
        let mid = (low + high) / 2.0;
        if contrast(ink, colors::mix_srgb(fill, toward, mid)) < READS {
            low = mid;
        } else {
            high = mid;
        }
    }
    colors::mix_srgb(fill, toward, high)
}

fn contrast(a: Color, b: Color) -> f32 {
    let (a, b) = (a.luminance(), b.luminance());
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}
