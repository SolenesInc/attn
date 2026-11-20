use std::any::Any;
use std::rc::Rc;

use rune::{Color, Cx, El, Tx, col, row};
use rune_kit::ScrollBar;

use crate::dialog::Dialog;
use crate::system::{
    Button, IconSize, Look, RING_REACH, Radius, Said, Size, Space, Type, door, focus, layers,
    lines_at, width_of, words,
};
use crate::{DialogFrame, Tokens, colors, icon};

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
type Press = Box<dyn FnOnce(Button) -> Button>;
/// What closes the dialog a control is pressed in, over the window's state.
type Close = Rc<dyn Fn(&mut dyn Any, &mut Tx)>;
/// A control's press, which also closes the dialog it is drawn in, if any.
type Acts<T> = Box<dyn FnOnce(T, Option<Close>) -> T>;
type OnPress<S> = Box<dyn Fn(&mut S, &mut Tx)>;

struct Sentence {
    key: String,
    said: Said,
}

enum Note {
    Words {
        key: String,
        said: Said,
    },
    Link {
        key: String,
        shown: Said,
        label: String,
        press: Acts<El>,
    },
    Button {
        key: String,
        label: String,
        button: Box<Button>,
        press: Acts<Button>,
    },
}

/// The bar under the title bar that tells the person about something the whole window cares
/// about: a sentence, its actions right beside it, its notes after them or on a line of their
/// own, and dismiss alone at the far edge. The glyph, the actions and dismiss sit on the
/// sentence's first line. The bar never grows past its lines: what it cuts short, Details opens
/// whole in a dialog.
pub struct Banner {
    key: String,
    label: String,
    tone: BannerTone,
    value: Option<String>,
    sentence: Option<Sentence>,
    details: Option<Press>,
    items: Vec<Vec<Note>>,
    actions: Vec<(String, Button, Acts<Button>)>,
    countdown: Option<(String, u32)>,
    dismiss: Option<(String, String, Attach)>,
    hover: Option<Attach>,
}

/// How much of its tone a banner's fill takes over the app's background: the least share at
/// which every tone stands further from the title bar than a hover step, in dark and in light.
const TINT: f32 = 0.18;
const EDGE: f32 = 0.45;
/// WCAG's AA contrast for text below 18 points.
const READS: f32 = 4.5;
/// The least width a sentence keeps beside its actions: 40 characters, the shortest line that
/// still reads as one, which the banners' real sentences in Name (6.24 to 6.63 a character)
/// measure 250 across. Actions that would leave it less, or leave it more lines than it shows,
/// take a line of their own.
const READABLE: f32 = 250.0;
/// The most lines a closed sentence shows: one past the longest message a banner shows in use,
/// a self-update that could not stage one outpost with its log's path, which takes 3 lines at
/// the smallest window, 800 x 600 at 150%.
const LINES: usize = 4;
/// The most lines a closed bar gives its notes: the fewest that hold whole one blocker of the
/// shared Codex restart, named by an 86-character worktree and still starting ("Waiting on" and
/// its name, its reason, Relaunch), at the smallest window, 800 x 600 at 150%. Its bar leaves
/// 252 of the 368 points under the title bar to work in, and so does a bar of twelve such
/// blockers; with two lines the bar dropped the blocker's name.
const NOTE_LINES: usize = 3;

impl Banner {
    pub fn new(key: impl Into<String>, label: impl Into<String>, tone: BannerTone) -> Banner {
        Banner {
            key: key.into(),
            label: label.into(),
            tone,
            value: None,
            sentence: None,
            details: None,
            items: Vec::new(),
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

    /// What the banner says. A sentence longer than its lines ends in an ellipsis.
    pub fn says(mut self, key: impl Into<String>, sentence: impl Into<Said>) -> Self {
        self.sentence = Some(Sentence {
            key: key.into(),
            said: sentence.into(),
        });
        self
    }

    /// Opens the dialog that holds the banner whole, offered last among the actions while the bar
    /// may cut something short: a sentence past its lines, a note wider than its line, items past
    /// the bar's lines, or a note that holds a control, whose name the bar may elide.
    pub fn details<S: 'static>(mut self, open: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.details = Some(Box::new(move |b: Button| b.on_press_tx(open)));
        self
    }

    /// Starts the next item: the notes after it read as one, which the bar names whole or counts
    /// among those it leaves to Details. Notes before any item are the first.
    pub fn item(mut self) -> Self {
        if self.items.last().is_none_or(|item| !item.is_empty()) {
            self.items.push(Vec::new());
        }
        self
    }

    fn push(&mut self, note: Note) {
        match self.items.last_mut() {
            Some(item) => item.push(note),
            None => self.items.push(vec![note]),
        }
    }

    /// Quieter words after the sentence and its actions. Notes that do not fit there move
    /// together to lines of their own under the sentence, and a note longer than a line ends
    /// in an ellipsis. The bar names the items that fit its lines, the first at least in part,
    /// and counts the rest.
    pub fn note(mut self, key: impl Into<String>, said: impl Into<Said>) -> Self {
        self.push(Note::Words {
            key: key.into(),
            said: said.into(),
        });
        self
    }

    /// A name among the notes that takes the person to it, announced as `label`.
    pub fn link<S: 'static>(
        mut self,
        key: impl Into<String>,
        shown: impl Into<Said>,
        label: impl Into<String>,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Self {
        self.push(Note::Link {
            key: key.into(),
            shown: shown.into(),
            label: label.into(),
            press: acts(press, |e: El, press| e.on_press_tx(press)),
        });
        self
    }

    /// A button among the notes, for what one of them offers, announced as `label`.
    pub fn note_button<S: 'static>(
        mut self,
        key: impl Into<String>,
        label: impl Into<String>,
        button: Button,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Self {
        self.push(Note::Button {
            key: key.into(),
            label: label.into(),
            button: Box::new(button),
            press: acts(press, |b: Button, press| b.on_press_tx(press)),
        });
        self
    }

    /// What the banner offers, right after its sentence, in the order given: the action that
    /// moves things forward first. The first action of a banner that needs the person is
    /// primary.
    pub fn action<S: 'static>(
        mut self,
        key: impl Into<String>,
        button: Button,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Self {
        self.actions.push((
            key.into(),
            button,
            acts(press, |b: Button, press| b.on_press_tx(press)),
        ));
        self
    }

    /// Seconds until the banner closes on its own.
    pub fn countdown(mut self, key: impl Into<String>, seconds: u32) -> Self {
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

    /// The bar, laid out from the width `cx` last gave its words, which only the bar decides, so
    /// Details shows only while the sentence takes more than its lines there.
    pub fn build<S>(self, t: &Tokens, cx: &Cx<S>) -> El {
        let paint = Paint::of(self.tone, t);
        let band = Size::Compact.height();
        let glyph = row()
            .h(band)
            .items_center()
            .a11y_hidden()
            .child(door::icon(glyph(self.tone), IconSize::Regular).color(paint.glyph));
        let column_key = format!("{}.said", self.key);
        let column = cx.layout_with(&column_key, |r| r[2]);
        let between = if self
            .items
            .iter()
            .flatten()
            .all(|n| matches!(n, Note::Words { .. }))
        {
            Space::S8
        } else {
            Space::S4
        };
        let (notes, more, cut) = named(
            self.items,
            column.unwrap_or(READABLE),
            between.pt(),
            &self.key,
        );
        let mut cut_short = cut || notes.iter().any(|note| !matches!(note, Note::Words { .. }));
        let sentence = self.sentence.map(|sentence| {
            let natural = width_of(&sentence.said, Type::Name);
            let least = match column {
                Some(column) => least_width(&sentence.said, natural).min(column),
                None => natural.min(READABLE),
            };
            cut_short |=
                column.is_some_and(|w| lines_at(&sentence.said, Type::Name, w) > LINES as f32);
            let said = words(sentence.said, Type::Name, paint.ink)
                .key(sentence.key)
                .max_lines(LINES);
            door::first_line_in(
                col().grow(1.0).min_w(least).max_w(natural).child(said),
                Type::Name,
                band,
            )
        });
        cut_short |= column.is_some_and(|w| {
            notes.iter().any(
                |note| matches!(note, Note::Words { said, .. } if width_of(said, Type::Body) > w),
            )
        });
        let details = self.details.filter(|_| cut_short).map(|press| {
            let details = Button::new("Details").look(Look::Quiet).size(Size::Compact);
            let details = press(details).build(&paint.controls);
            row()
                .shrink()
                .child(El::from(details.key(format!("{}.details", self.key))))
        });
        let actions = actions(self.tone, self.actions, &paint.controls, None)
            .into_iter()
            .map(|button| row().shrink().child(button));
        let countdown = self.countdown.map(|(key, seconds)| {
            let said = words(format!("Closing in {seconds}s"), Type::Body, paint.quiet)
                .key(key)
                .single_line();
            door::first_line_in(row().child(said), Type::Body, band)
        });
        let actions: Vec<El> = actions.chain(details).chain(countdown).collect();
        let actions = (!actions.is_empty()).then(|| {
            door::gaps(row().flex_wrap().items_start(), Space::S8, Space::S8)
                .shrink()
                .children(actions)
        });
        let head = door::gaps(row().flex_wrap().items_start(), Space::S8, Space::S8)
            .shrink()
            .children(sentence)
            .children(actions);
        let notes = (!notes.is_empty() || more.is_some()).then(|| {
            let notes = notes
                .into_iter()
                .chain(more)
                .map(|note| paint.note(note, None));
            let notes = row()
                .flex_wrap()
                .items_center()
                .min_h(band)
                .shrink()
                .children(notes);
            door::gaps(notes, Space::S4, between)
        });
        let said = door::gaps(row().flex_wrap().items_start(), Space::S0, Space::S8)
            .key(column_key)
            .grow(1.0)
            .shrink()
            .child(head)
            .children(notes);
        let dismiss = self
            .dismiss
            .map(|(key, label, press)| paint.dismiss(key, label, press));
        let bar = row()
            .key(self.key)
            .role("region")
            .label(self.label)
            .w_full()
            .items_start()
            .fill(paint.fill)
            .border_sides([0.0, 0.0, 1.0, 0.0], paint.edge)
            .child(glyph)
            .child(said)
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

    /// The dialog that holds the banner whole: its sentence, every note and its actions, under
    /// its label, closed by `close`, as pressing any control in it also does. It is keyed `<banner>.whole`, and each part in it by its key
    /// in the bar after `whole.`.
    pub fn build_details<S: 'static>(
        self,
        t: &Tokens,
        close: impl Fn(&mut S, &mut Tx) + Clone + 'static,
        layers: &mut Vec<El>,
    ) {
        let paint = Paint::within(t);
        let closing: Close = {
            let close = close.clone();
            Rc::new(move |state: &mut dyn Any, tx: &mut Tx| {
                if let Some(state) = state.downcast_mut::<S>() {
                    close(state, tx);
                }
            })
        };
        let sentence = self.sentence.map(|sentence| {
            words(sentence.said, Type::Name, paint.ink)
                .key(whole(&sentence.key))
                .wrap()
        });
        let notes = (!self.items.is_empty()).then(|| {
            let notes = self
                .items
                .into_iter()
                .flatten()
                .map(|note| paint.note(note.within(), Some(closing.clone())));
            door::gaps(
                row().flex_wrap().items_center().w_full(),
                Space::S4,
                Space::S4,
            )
            .children(notes)
        });
        let said = door::gap(col().w_full(), Space::S12)
            .children(sentence)
            .children(notes);
        let said = door::inset(said, Space::S24, Space::S4);
        let said = col()
            .key(format!("{}.whole.said", self.key))
            .w_full()
            .shrink()
            .min_h(0.0)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .scroll_margin(RING_REACH)
            .child(said)
            .scroll_bar_styled(crate::scroll_bar(t));
        let actions =
            door::gap(row().w_full().justify_end().flex_wrap(), Space::S8).children(actions(
                self.tone,
                self.actions
                    .into_iter()
                    .map(|(key, button, press)| (whole(&key), button, press))
                    .collect(),
                t,
                Some(closing),
            ));
        let actions = door::inset(actions, Space::S24, Space::S16);
        Dialog::new(
            format!("{}.whole", self.key),
            self.label,
            DialogFrame::prompt(t),
        )
        .width(560.0)
        .on_close_tx(close)
        .child(said)
        .child(actions)
        .build(t, layers);
    }
}

fn whole(key: &str) -> String {
    format!("whole.{key}")
}

/// The notes the bar names within its lines at `width`: the items that fit whole, the first
/// item's leading notes when it alone is longer, a note counting the items left to Details,
/// and whether it left anything out.
fn named(
    items: Vec<Vec<Note>>,
    width: f32,
    gap: f32,
    key: &str,
) -> (Vec<Note>, Option<Note>, bool) {
    let widths: Vec<Vec<f32>> = items
        .iter()
        .map(|item| item.iter().map(|note| note.width().min(width)).collect())
        .collect();
    let fits = |shown: &[f32]| lines_of(shown, width, gap) <= NOTE_LINES;
    let whole_items = |k: usize| widths[..k].concat();
    let mut k = 0;
    while k < items.len() && fits(&whole_items(k + 1)) {
        k += 1;
    }
    if k == items.len() {
        return (items.into_iter().flatten().collect(), None, false);
    }
    let counted = |left: usize| width_of(counting(left), Type::Body).min(width);
    while k > 0 && !fits(&[whole_items(k), vec![counted(items.len() - k)]].concat()) {
        k -= 1;
    }
    let mut items = items.into_iter();
    let mut shown: Vec<Note> = items.by_ref().take(k).flatten().collect();
    let mut named = k;
    if k == 0 {
        let first = items.next().unwrap_or_default();
        let left = (widths.len() > 1).then(|| counted(widths.len() - 1));
        let leading = (0..=first.len())
            .rev()
            .find(|&m| fits(&[&widths[0][..m], left.as_slice()].concat()))
            .unwrap_or(0);
        shown.extend(first.into_iter().take(leading));
        named = 1;
    }
    let left = widths.len() - named;
    let more = (left > 0).then(|| Note::Words {
        key: format!("{key}.more"),
        said: counting(left).into(),
    });
    (shown, more, true)
}

fn counting(left: usize) -> String {
    format!("and {left} more.")
}

/// How many lines notes as wide as `widths` wrap to across `width`, `gap` apart.
fn lines_of(widths: &[f32], width: f32, gap: f32) -> usize {
    let mut lines = 0;
    let mut x = 0.0;
    for &w in widths {
        if lines == 0 || x + gap + w > width + 0.5 {
            lines += 1;
            x = w;
        } else {
            x += gap + w;
        }
    }
    lines
}

impl Note {
    fn width(&self) -> f32 {
        match self {
            Note::Words { said, .. } => width_of(said, Type::Body),
            Note::Link { shown, .. } => width_of(shown, Type::Name),
            Note::Button { button, .. } => button.natural_width(Size::Compact),
        }
    }

    fn within(self) -> Note {
        match self {
            Note::Words { key, said } => Note::Words {
                key: whole(&key),
                said,
            },
            Note::Link {
                key,
                shown,
                label,
                press,
            } => Note::Link {
                key: whole(&key),
                shown,
                label,
                press,
            },
            Note::Button {
                key,
                label,
                button,
                press,
            } => Note::Button {
                key: whole(&key),
                label,
                button,
                press,
            },
        }
    }
}

/// `press`, attached by `on`, after `close` when the control is drawn in a dialog, so focus the
/// press moves stays where it put it.
fn acts<S: 'static, T: 'static>(
    press: impl Fn(&mut S, &mut Tx) + 'static,
    on: fn(T, OnPress<S>) -> T,
) -> Acts<T> {
    Box::new(move |control: T, close: Option<Close>| {
        on(
            control,
            Box::new(move |state: &mut S, tx: &mut Tx| {
                if let Some(close) = &close {
                    close(state, tx);
                }
                press(state, tx);
            }),
        )
    })
}

/// A banner's actions, in the order given, the first primary when the banner needs the person.
fn actions(
    tone: BannerTone,
    actions: Vec<(String, Button, Acts<Button>)>,
    t: &Tokens,
    close: Option<Close>,
) -> Vec<El> {
    actions
        .into_iter()
        .enumerate()
        .map(|(i, (key, button, press))| {
            let button = press(button, close.clone());
            let look = if tone == BannerTone::Warn && i == 0 {
                Look::Primary
            } else {
                Look::Plain
            };
            El::from(button.look(look).size(Size::Compact).build(t).key(key))
        })
        .collect()
}

/// The least width a sentence `natural` wide keeps beside its actions: readable, and wide
/// enough to show it within its lines when its own width can, so actions move under it before
/// they would cut it short.
fn least_width(said: &Said, natural: f32) -> f32 {
    let fits = |w: f32| lines_at(said, Type::Name, w) <= LINES as f32;
    if natural <= READABLE || fits(READABLE) {
        return natural.min(READABLE);
    }
    let (mut short, mut whole) = (READABLE, natural);
    while whole - short > 1.0 {
        let mid = (short + whole) / 2.0;
        if fits(mid) {
            whole = mid;
        } else {
            short = mid;
        }
    }
    whole.ceil()
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
    whole: bool,
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
                controls: Tokens {
                    warning: ink,
                    text_secondary: ink,
                    text_tertiary: ink,
                    ..*t
                },
                whole: false,
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
            whole: false,
        }
    }

    fn within(t: &Tokens) -> Paint {
        Paint {
            fill: t.bg_panel,
            edge: t.border,
            glyph: t.text_tertiary,
            ink: t.text_primary,
            quiet: t.text_secondary,
            link: t.accent,
            controls: *t,
            whole: true,
        }
    }

    /// Words a note shows: on one line ending in an ellipsis in the bar, whole in the details.
    fn fit(&self, words: El) -> El {
        if self.whole {
            words.wrap().shrink()
        } else {
            words.truncate()
        }
    }

    fn note(&self, note: Note, close: Option<Close>) -> El {
        let t = &self.controls;
        match note {
            Note::Words { key, said } => self
                .fit(words(said, Type::Body, self.quiet).key(key))
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
                    .shrink()
                    .child(self.fit(words(shown, Type::Name, self.link)).a11y_hidden());
                let link = door::corner(link, Radius::Tag);
                focus::ring(press(layers::pressable(link, self.fill, t), close), t)
            }
            Note::Button {
                key,
                label,
                button,
                press,
            } => {
                let button = press(*button, close)
                    .size(Size::Compact)
                    .build(t)
                    .key(key)
                    .label(label);
                row().shrink().child(El::from(button))
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
            .child(door::icon(&rune_kit::glyphs::CLOSE, IconSize::Large).color(self.quiet));
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
