use rune::{Color, El, el, row};

use super::block::{Block, Kind};
use super::measure::COPY_FLOOR;
use crate::Tokens;
use crate::system::{Radius, Size, Space, Tone, Type, door, words};

const DOT: f32 = 6.0;
const EDGE: f32 = 0.45;
const WASH: f32 = 0.08;

/// A line across the page that says what is happening or what went wrong, with what to do
/// about it.
pub struct Notice {
    key: String,
    said: String,
    tone: Tone,
    action: Option<El>,
}

impl Notice {
    pub fn error(key: impl Into<String>, said: impl Into<String>) -> Notice {
        Notice::new(key, said, Tone::Bad)
    }

    pub fn warn(key: impl Into<String>, said: impl Into<String>) -> Notice {
        Notice::new(key, said, Tone::Warn)
    }

    pub fn info(key: impl Into<String>, said: impl Into<String>) -> Notice {
        Notice::new(key, said, Tone::Neutral)
    }

    fn new(key: impl Into<String>, said: impl Into<String>, tone: Tone) -> Notice {
        Notice {
            key: key.into(),
            said: said.into(),
            tone,
            action: None,
        }
    }

    /// The button that deals with it, such as Retry, at the line's end.
    pub fn action(mut self, button: impl Into<El>) -> Self {
        self.action = Some(button.into());
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let (ink, edge, fill) = match self.tone {
            Tone::Bad => (t.text_primary, t.error.alpha(EDGE), t.error.alpha(WASH)),
            Tone::Warn => (t.text_primary, t.warning.alpha(EDGE), t.warning.alpha(WASH)),
            Tone::Good => (t.text_primary, t.success.alpha(EDGE), t.success.alpha(WASH)),
            Tone::Neutral => (t.text_secondary, t.border, t.bg_elevated),
        };
        let role = match self.tone {
            Tone::Bad | Tone::Warn => "alert",
            Tone::Good | Tone::Neutral => "status",
        };
        let mark = row()
            .h(Size::Regular.height())
            .items_center()
            .child(dot(tone_ink(self.tone, t)));
        let said = words(self.said.clone(), Type::Small, ink)
            .wrap()
            .a11y_hidden();
        let said = door::gaps(
            row().grow(1.0).min_w(0.0).flex_wrap().items_start(),
            Space::S8,
            Space::S8,
        )
        .child(door::on_band(said, Type::Small, Size::Regular).grow_from(1.0, COPY_FLOOR))
        .children(self.action);
        let line = row()
            .key(self.key)
            .role(role)
            .label(self.said.clone())
            .w_full()
            .items_start()
            .fill(fill)
            .border(1.0, edge)
            .child(mark)
            .child(said);
        let line = door::gap(door::inset(line, Space::S12, Space::S8), Space::S8);
        Block::new(Kind::Notice, door::corner(line, Radius::Card))
    }
}

/// The line under a list that says how it adds up, with a dot for its state, and a quiet aside
/// at its end.
pub struct Status {
    key: String,
    said: String,
    live: bool,
    aside: Option<String>,
}

impl Status {
    pub fn new(key: impl Into<String>, said: impl Into<String>, live: bool) -> Status {
        Status {
            key: key.into(),
            said: said.into(),
            live,
            aside: None,
        }
    }

    pub fn aside(mut self, aside: impl Into<String>) -> Self {
        self.aside = Some(aside.into());
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let said = row()
            .key(self.key)
            .role("status")
            .label(self.said.clone())
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

/// Why a value was refused, beside the control that holds it, with a way to try again.
pub struct Refusal {
    key: String,
    reason: String,
    retry: Option<El>,
}

impl Refusal {
    pub fn new(key: impl Into<String>, reason: impl Into<String>) -> Refusal {
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
        let line = row()
            .key(self.key)
            .role("alert")
            .label(self.reason.clone())
            .items_center()
            .child(words(self.reason, Type::Small, t.error).wrap())
            .children(self.retry);
        door::gap(line, Space::S8)
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
        Tone::Neutral => t.text_tertiary,
    }
}

fn dot(ink: Color) -> El {
    door::corner(el().size(DOT, DOT), Radius::Round)
        .fill(ink)
        .a11y_hidden()
}
