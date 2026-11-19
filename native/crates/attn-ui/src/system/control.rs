use rune::El;

use super::Size;

/// A control built by attn-ui. It carries its size, so whatever holds it can read the inset its
/// words sit at.
pub struct Control {
    el: El,
    size: Size,
}

impl Control {
    pub(crate) fn new(el: El, size: Size) -> Control {
        Control { el, size }
    }

    pub fn size(&self) -> Size {
        self.size
    }

    pub fn key(self, key: impl Into<String>) -> Control {
        Control {
            el: self.el.key(key.into()),
            ..self
        }
    }

    pub fn label(self, label: impl Into<String>) -> Control {
        Control {
            el: self.el.label(label.into()),
            ..self
        }
    }
}

impl From<Control> for El {
    fn from(control: Control) -> El {
        control.el
    }
}
