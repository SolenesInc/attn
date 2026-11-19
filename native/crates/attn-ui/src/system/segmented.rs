use rune::{Color, El, Tx, row};

use super::{Control, Radius, Size, Space, Type, door, focus, layers, words};
use crate::Tokens;

/// One choice among a few, all in view: Left and Right move it.
pub struct Segmented {
    key: String,
    label: String,
    options: Vec<Segment>,
}

/// One option of a [`Segmented`].
pub struct Segment {
    key: String,
    name: String,
    chosen: bool,
    disabled: bool,
    help: Option<String>,
    trailing: Option<El>,
    press: Option<Box<dyn FnOnce(El) -> El>>,
}

impl Segment {
    pub fn new(key: impl Into<String>, name: impl Into<String>) -> Segment {
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

    fn build(self, (i, n): (usize, usize), t: &Tokens) -> El {
        let (fill, ink) = if self.chosen {
            (t.bg_selected, t.text_primary)
        } else {
            (Color::TRANSPARENT, t.text_secondary)
        };
        let option = row()
            .key(self.key)
            .role("radio")
            .label(self.name.clone())
            .checked(self.chosen)
            .position_in_set(i + 1, n)
            .focusable(true)
            .focus_selected(self.chosen)
            .disabled(self.disabled)
            .min_h(Size::Compact.height())
            .shrink()
            .min_w(0.0)
            .center()
            .fill(fill)
            .child(
                words(self.name, Type::Control, ink)
                    .wrap()
                    .align(rune::Align::Center)
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
            return option.layer_opacity(DISABLED);
        }
        let option = layers::pressable(option, ink);
        match self.press {
            Some(press) => press(option),
            None => option,
        }
    }
}

const DISABLED: f32 = 0.45;

impl Segmented {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Segmented {
        Segmented {
            key: key.into(),
            label: label.into(),
            options: Vec::new(),
        }
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
            .items_center()
            .fill(t.bg_input)
            .border(1.0, t.border)
            .children(
                self.options
                    .into_iter()
                    .enumerate()
                    .map(|(i, option)| option.build((i, n), t)),
            );
        let track = door::inset(door::gap(track, Space::S2), Space::S2, Space::S2);
        let line = row()
            .w_full()
            .min_w(0.0)
            .child(door::corner(track, Radius::Card));
        Control::new(line, Size::Regular)
    }
}
