use rune::{Color, El, Tx, row};

use super::{Control, Radius, Said, Size, Space, Type, disabled, door, focus, layers, words};
use crate::Tokens;

/// One choice among a few, all in view: Left and Right move it.
pub struct Segmented {
    key: String,
    label: String,
    options: Vec<Segment>,
    disabled: bool,
    tab_stops: bool,
}

/// One option of a [`Segmented`].
pub struct Segment {
    key: String,
    name: Said,
    chosen: bool,
    disabled: bool,
    help: Option<String>,
    trailing: Option<El>,
    press: Option<Box<dyn FnOnce(El) -> El>>,
}

impl Segment {
    pub fn new(key: impl Into<String>, name: impl Into<Said>) -> Segment {
        Segment {
            key: key.into(),
            name: name.into(),
            chosen: false,
            disabled: false,
            help: None,
            trailing: None,
            press: None,
        }
    }

    pub fn chosen(mut self, chosen: bool) -> Self {
        self.chosen = chosen;
        self
    }

    /// Shown but not chosen by a press: what it needs is missing.
    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    /// Why it is the way it is, as a tooltip and its accessible description.
    pub fn help(mut self, help: impl Into<String>) -> Self {
        self.help = Some(help.into());
        self
    }

    /// A mark after the name, such as a sign that it is still being checked.
    pub fn trailing(mut self, mark: El) -> Self {
        self.trailing = Some(mark);
        self
    }

    pub fn on_press_tx<S: 'static>(mut self, press: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.press = Some(Box::new(move |e: El| e.on_press_tx(press)));
        self
    }

    fn build(self, (i, n): (usize, usize), track_disabled: bool, tab_stop: bool, t: &Tokens) -> El {
        let (fill, ink) = if self.chosen {
            (t.bg_selected, t.text_primary)
        } else {
            (Color::TRANSPARENT, t.text_secondary)
        };
        let option = row()
            .key(self.key)
            .role("radio")
            .label(self.name.text())
            .checked(self.chosen)
            .position_in_set(i + 1, n)
            .focusable(tab_stop)
            .focus_selected(self.chosen)
            .min_h(Size::Compact.height())
            .shrink()
            .min_w(0.0)
            .center()
            .fill(fill)
            .child(
                words(self.name, Type::Control, ink)
                    .truncate()
                    .shrink()
                    .min_w(0.0)
                    .a11y_hidden(),
            )
            .children(self.trailing);
        let option = door::inset(option, Size::Compact.inset(), Space::S4);
        let option = door::gap(door::corner(option, Radius::Control), Space::S4);
        let option = focus::ring(option, t);
        let option = if self.chosen {
            option.border(1.0, t.border_hover)
        } else {
            option
        };
        let option = match self.help {
            Some(help) => option.help(help.clone()).description(help),
            None => option,
        };
        if self.disabled {
            return disabled::dim(option);
        }
        if track_disabled {
            return option.disabled(true);
        }
        let option = layers::pressable(option, if self.chosen { fill } else { t.bg_input }, t);
        match self.press {
            Some(press) => press(option),
            None => option,
        }
    }
}

impl Segmented {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Segmented {
        Segmented {
            key: key.into(),
            label: label.into(),
            options: Vec::new(),
            disabled: false,
            tab_stops: true,
        }
    }

    /// The keys that choose live elsewhere, such as Alt and a letter beside each name, so no
    /// option takes a Tab stop.
    pub fn without_tab_stops(mut self) -> Self {
        self.tab_stops = false;
        self
    }

    /// The whole control can't be changed now: every option is drawn and read as unavailable.
    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    pub fn option(mut self, option: Segment) -> Self {
        self.options.push(option);
        self
    }

    pub fn options(mut self, options: impl IntoIterator<Item = Segment>) -> Self {
        self.options.extend(options);
        self
    }

    pub fn build(self, t: &Tokens) -> Control {
        let n = self.options.len();
        let track = row()
            .key(self.key)
            .role("radiogroup")
            .label(self.label)
            .focus_group()
            .shrink()
            .min_w(0.0)
            .max_w_frac(1.0)
            .items_center()
            .fill(t.bg_input)
            .border(1.0, t.border)
            .children(
                self.options
                    .into_iter()
                    .enumerate()
                    .map(|(i, option)| option.build((i, n), self.disabled, self.tab_stops, t)),
            );
        let track = door::inset(door::gap(track, Space::S2), Space::S2, Space::S2);
        let track = door::corner(track, Radius::Card);
        let track = if self.disabled {
            disabled::dim(track)
        } else {
            track
        };
        Control::new(track, Size::Regular)
    }
}
