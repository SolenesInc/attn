use rune::{El, Tx, col, el, row};

use super::block::{Block, Kind};
use crate::Tokens;
use crate::system::{Button, IconSize, Look, Radius, Size, Space, Type, door, words};

/// How tall the line that shows how far a clip has played stands.
const TRACK_HIGH: f32 = 4.0;

/// Where a clip stands.
#[derive(Clone, Debug, PartialEq)]
pub enum Playing {
    /// Its sound is still on its way.
    Loading,
    Paused,
    Playing,
    /// It cannot play here, and why.
    Unplayable(String),
}

/// A sound clip with its name, one button that plays and pauses it, and how far it has played.
pub struct Audio {
    key: String,
    label: String,
    playing: Playing,
    at: f64,
    length: Option<f64>,
    highlighted: bool,
    toggle: Option<Box<dyn FnOnce(Button) -> Button>>,
}

impl Audio {
    /// A clip keyed `key`, named `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Audio {
        Audio {
            key: key.into(),
            label: label.into(),
            playing: Playing::Loading,
            at: 0.0,
            length: None,
            highlighted: false,
            toggle: None,
        }
    }

    pub fn playing(mut self, playing: Playing) -> Self {
        self.playing = playing;
        self
    }

    /// It has played `at` seconds of `length`, once its length is known.
    pub fn time(mut self, at: f64, length: Option<f64>) -> Self {
        self.at = at;
        self.length = length;
        self
    }

    /// The keys act on it: it stands out as a list's highlighted row does.
    pub fn highlighted(mut self, highlighted: bool) -> Self {
        self.highlighted = highlighted;
        self
    }

    /// What its button does: play it while it is paused, and pause it while it plays.
    pub fn on_toggle<S: 'static>(mut self, toggle: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.toggle = Some(Box::new(move |b: Button| b.on_press_tx(toggle)));
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Custom, self.el(t))
    }

    pub fn el(self, t: &Tokens) -> El {
        let (mark, verb) = match self.playing {
            Playing::Playing => (crate::glyph::PAUSE, "Pause"),
            _ => (crate::glyph::PLAY, "Play"),
        };
        let button = Button::mark(mark, IconSize::Regular)
            .look(Look::Plain)
            .size(Size::Regular)
            .disabled(matches!(
                self.playing,
                Playing::Loading | Playing::Unplayable(_)
            ));
        let button = match self.toggle {
            Some(toggle) => toggle(button),
            None => button,
        };
        let button = El::from(button.build(t))
            .key(format!("{}.toggle", self.key))
            .label(format!("{verb} {}", self.label));
        let said = match &self.playing {
            Playing::Unplayable(why) => why.clone(),
            Playing::Loading => "Loading".to_owned(),
            Playing::Paused | Playing::Playing => match self.length {
                Some(length) => format!("{} / {}", clock(self.at), clock(length)),
                None => clock(self.at),
            },
        };
        let state = match &self.playing {
            Playing::Playing => "Playing",
            Playing::Paused => "Paused",
            Playing::Loading | Playing::Unplayable(_) => "",
        };
        let value = [state, said.as_str()]
            .into_iter()
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join(", ");
        let played = match (&self.playing, self.length) {
            (Playing::Paused | Playing::Playing, Some(length)) if length > 0.0 => {
                (self.at / length).clamp(0.0, 1.0) as f32
            }
            _ => 0.0,
        };
        let track = door::corner(
            row()
                .key(format!("{}.track", self.key))
                .w_full()
                .h(TRACK_HIGH)
                .fill(t.border)
                .a11y_hidden()
                .child(el().grow(played).h(TRACK_HIGH).fill(t.accent))
                .child(el().grow(1.0 - played).h(TRACK_HIGH)),
            Radius::Card,
        );
        let ink = match self.playing {
            Playing::Unplayable(_) => t.text_tertiary,
            _ => t.text_secondary,
        };
        let about = door::gap(col().grow(1.0).min_w(0.0), Space::S4)
            .child(
                words(self.label.clone(), Type::Name, t.text_primary)
                    .single_line()
                    .truncate(),
            )
            .child(track)
            .child(words(said, Type::Small, ink).single_line().a11y_hidden());
        let line = door::gap(row().key(self.key).w_full().items_center(), Space::S12)
            .role("group")
            .label(self.label)
            .a11y_value(value)
            .child(button)
            .child(about);
        let line = match self.highlighted {
            true => line.fill(t.bg_selected),
            false => line,
        };
        door::corner(door::inset(line, Space::S8, Space::S8), Radius::Card)
    }
}

/// `seconds` as a clock reads them: minutes and seconds, and hours once there are any.
fn clock(seconds: f64) -> String {
    let whole = seconds.max(0.0).floor() as u64;
    let (h, m, s) = (whole / 3600, whole / 60 % 60, whole % 60);
    match h {
        0 => format!("{m}:{s:02}"),
        h => format!("{h}:{m:02}:{s:02}"),
    }
}
