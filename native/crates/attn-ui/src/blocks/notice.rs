use rune::{Color, El, col, el, row};
use rune_kit::ScrollBar;

use super::block::{Block, Kind};
use super::measure::COPY_FLOOR;
use crate::Tokens;
use crate::system::{Radius, Said, Size, Space, Tone, Type, door, focus, words};

const DOT: f32 = 6.0;
const EDGE: f32 = 0.45;
const WASH: f32 = 0.08;
/// The room between a notice's edge and what it holds. A pinned notice's sentence scrolls
/// inside the room at its end, so its scroll bar stands beside the words, never over them.
const NOTICE_INSET_X: Space = Space::S12;
const NOTICE_INSET_Y: Space = Space::S8;

/// A line across the page that says what is happening or what went wrong, with what to do
/// about it.
pub struct Notice {
    key: String,
    said: Said,
    tone: Tone,
    action: Option<El>,
    bare: bool,
    action_height: Option<f32>,
}

impl Notice {
    pub fn error(key: impl Into<String>, said: impl Into<Said>) -> Notice {
        Notice::new(key, said, Tone::Bad)
    }

    pub fn warn(key: impl Into<String>, said: impl Into<Said>) -> Notice {
        Notice::new(key, said, Tone::Warn)
    }

    pub fn info(key: impl Into<String>, said: impl Into<Said>) -> Notice {
        Notice::new(key, said, Tone::Neutral)
    }

    /// News the app brings itself, such as what a release changed: in the brand's tone, and
    /// announced politely.
    pub fn accent(key: impl Into<String>, said: impl Into<Said>) -> Notice {
        Notice::new(key, said, Tone::Accent)
    }

    fn new(key: impl Into<String>, said: impl Into<Said>, tone: Tone) -> Notice {
        Notice {
            key: key.into(),
            said: said.into(),
            tone,
            action: None,
            bare: false,
            action_height: None,
        }
    }

    /// The button that deals with it, such as Retry, at the line's end.
    pub fn action(mut self, button: impl Into<El>) -> Self {
        self.action = Some(button.into());
        self
    }

    /// How tall the action stood as last laid out, when it can wrap onto more lines than one,
    /// such as two buttons in a narrow pane: a pinned notice keeps room for all of it. Nothing,
    /// or 0 before it is laid out, counts one line of compact buttons.
    pub fn action_height(mut self, points: f32) -> Self {
        self.action_height = Some(points).filter(|h| *h > 0.0);
        self
    }

    /// The notice as its mark and words alone, with no card, for one that sits under what it
    /// is about, as a refusal under the strip that counts what was refused.
    pub fn bare(mut self) -> Self {
        self.bare = true;
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Notice, self.line(t, false))
    }

    /// The notice in a band that gives way to what it stands over: once the band is short, the
    /// sentence scrolls inside it down to its first line, and the action stays whole under it.
    pub(crate) fn pinned(self, t: &Tokens) -> El {
        let floor = self.floor();
        self.line(t, true).shrink().min_h(floor)
    }

    /// How short a pinned notice goes: its sentence's first line and its action, as tall as it
    /// was laid out or one line of compact buttons, inside its inset.
    pub(crate) fn floor(&self) -> f32 {
        let action = match self.action {
            Some(_) => Space::S8.pt() + self.action_height.unwrap_or(Size::Compact.height()),
            None => 0.0,
        };
        2.0 * NOTICE_INSET_Y.pt() + Size::Regular.height() + action
    }

    fn line(self, t: &Tokens, pinned: bool) -> El {
        let (ink, edge, fill) = match self.tone {
            Tone::Bad => (t.text_primary, t.error.alpha(EDGE), t.error.alpha(WASH)),
            Tone::Warn => (t.text_primary, t.warning.alpha(EDGE), t.warning.alpha(WASH)),
            Tone::Good => (t.text_primary, t.success.alpha(EDGE), t.success.alpha(WASH)),
            Tone::Accent => (t.text_primary, t.accent.alpha(EDGE), t.accent.alpha(WASH)),
            Tone::Neutral => (t.text_secondary, t.border, t.bg_elevated),
        };
        let role = match self.tone {
            Tone::Bad | Tone::Warn => "alert",
            Tone::Good | Tone::Neutral | Tone::Accent => "status",
        };
        let band = if self.bare {
            door::line_height(Type::Small)
        } else {
            Size::Regular.height()
        };
        let mark = row()
            .h(band)
            .items_center()
            .child(dot(tone_ink(self.tone, t)));
        let said = words(self.said.clone(), Type::Small, ink)
            .wrap_anywhere()
            .a11y_hidden();
        let said = if self.bare {
            said
        } else {
            door::on_band(said, Type::Small, Size::Regular)
        };
        let said = if pinned {
            let sentence_key = format!("{}.sentence", self.key);
            let words_key = format!("{sentence_key}.words");
            let overflows = rune::memo::layout(&sentence_key)
                .zip(rune::memo::layout(&words_key))
                .is_some_and(|(view, words)| words[3] > view[3] + 0.5);
            let sentence = col().key(sentence_key);
            let sentence = if overflows {
                focus::ring(
                    sentence
                        .role("group")
                        .label(self.said.text())
                        .focusable(true),
                    t,
                )
            } else {
                sentence
            };
            let sentence = sentence
                .shrink()
                .min_h(Size::Regular.height())
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .child(door::sides(
                    col().key(words_key).w_full().child(said),
                    Space::S0,
                    NOTICE_INSET_X,
                ))
                .scroll_bar_styled(crate::scroll_bar(t));
            door::gap(col().grow(1.0).min_w(0.0).min_h(0.0), Space::S8)
                .child(door::hang_right(sentence, NOTICE_INSET_X))
                .children(self.action.map(|action| action.w_full()))
        } else {
            door::gaps(
                row().grow(1.0).min_w(0.0).flex_wrap().items_start(),
                Space::S8,
                Space::S8,
            )
            .child(said.grow_from(1.0, COPY_FLOOR))
            .children(self.action)
        };
        let line = row()
            .key(self.key)
            .role(role)
            .label(self.said.text())
            .w_full();
        let line = if pinned {
            line.items_stretch()
        } else {
            line.items_start()
        };
        let line = line.child(mark).child(said);
        if self.bare {
            return door::gap(line, Space::S8);
        }
        let line = line.fill(fill).border(1.0, edge);
        let line = door::gap(door::inset(line, NOTICE_INSET_X, NOTICE_INSET_Y), Space::S8);
        door::corner(line, Radius::Card)
    }
}

/// The line under a list that says how it adds up, with a dot for its state, and a quiet aside
/// at its end.
pub struct Status {
    key: String,
    said: Said,
    live: bool,
    aside: Option<Said>,
}

impl Status {
    pub fn new(key: impl Into<String>, said: impl Into<Said>, live: bool) -> Status {
        Status {
            key: key.into(),
            said: said.into(),
            live,
            aside: None,
        }
    }

    pub fn aside(mut self, aside: impl Into<Said>) -> Self {
        self.aside = Some(aside.into());
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let said = row()
            .key(self.key)
            .role("status")
            .label(self.said.text())
            .grow(1.0)
            .min_w(0.0)
            .items_center()
            .child(dot(if self.live { t.success } else { t.text_dimmed }))
            .child(
                words(self.said, Type::Small, t.text_secondary)
                    .wrap()
                    .shrink()
                    .a11y_hidden(),
            );
        let line = door::gap(row().w_full().items_center(), Space::S8)
            .child(door::gap(said, Space::S8))
            .children(
                self.aside
                    .map(|aside| words(aside, Type::Small, t.text_tertiary)),
            );
        Block::new(Kind::Status, line)
    }
}

/// Why a value was refused, beside the control that holds it, with a way to try again. The
/// person's own action that caused it shows it with [`show_refusal`].
pub struct Refusal {
    key: String,
    reason: Said,
    retry: Option<El>,
}

impl Refusal {
    pub fn new(key: impl Into<String>, reason: impl Into<Said>) -> Refusal {
        Refusal {
            key: key.into(),
            reason: reason.into(),
            retry: None,
        }
    }

    pub fn retry(mut self, button: impl Into<El>) -> Self {
        self.retry = Some(button.into());
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let said = words(self.reason.clone(), Type::Small, t.error).wrap_anywhere();
        let said = if self.retry.is_some() {
            door::on_band(said, Type::Small, Size::Regular)
        } else {
            said
        };
        let line = row()
            .key(self.key)
            .role("alert")
            .label(self.reason.text())
            .w_full()
            .flex_wrap()
            .items_start()
            .child(said.grow_from(1.0, COPY_FLOOR))
            .children(self.retry);
        door::gaps(line, Space::S8, Space::S8)
    }
}

/// Shows a refusal the person caused while staying where they are: Enter in a field, a button's
/// press, a dialog's Save. It lands whole on the frame it appears, and the scrolling body keyed
/// `body_key` moves the least it can to show it, then to show `control_key`, the control the
/// person acted in, whole, on both axes, so they see why and keep their place. A refusal caused
/// by leaving a field for somewhere else never calls this, nor does one whose answer lands after
/// the person went elsewhere: the page stays where they are, and the refusal stands under what
/// caused it for when they look back. Nor does one that arrives unattended, from a poll, a load,
/// a timer, a reconnect or the daemon's own data: it never moves the page the person is reading.
/// `refusal_key` keys a [`Refusal`], an error [`Notice`] or a [`super::Field`]'s error.
pub fn show_refusal(
    tx: &mut rune::Tx,
    body_key: &str,
    refusal_key: impl Into<String>,
    control_key: Option<&str>,
) {
    tx.instant();
    tx.scroll_into_view_in(body_key, refusal_key);
    if let Some(control) = control_key {
        tx.scroll_into_view_in(body_key, control);
    }
}

/// The mark a control shows beside itself once its value is saved.
pub fn saved(key: impl Into<String>, t: &Tokens) -> El {
    words("Saved", Type::Small, t.success)
        .key(key.into())
        .role("status")
        .label("Saved")
}

fn tone_ink(tone: Tone, t: &Tokens) -> Color {
    match tone {
        Tone::Bad => t.error,
        Tone::Warn => t.warning,
        Tone::Good => t.success,
        Tone::Accent => t.accent,
        Tone::Neutral => t.text_tertiary,
    }
}

fn dot(ink: Color) -> El {
    door::corner(el().size(DOT, DOT), Radius::Round)
        .fill(ink)
        .a11y_hidden()
}
