//! The one module of attn-ui that calls Rune's raw spacing, font and corner methods. Every
//! other module, in this crate and in the app, names a token and comes through here.
#![allow(clippy::disallowed_methods)]

use rune::{El, Span, TextStyle};

use super::{Radius, Space, Type};

pub(crate) const fn style(kind: Type) -> TextStyle {
    match kind {
        Type::PageTitle => TextStyle::new(18.0, 700.0),
        Type::Heading => TextStyle::new(15.0, 600.0),
        Type::Name => TextStyle::new(13.0, 600.0),
        Type::Body => TextStyle::new(13.0, 400.0),
        Type::Small => TextStyle::new(12.0, 400.0),
        Type::Label => TextStyle::new(11.0, 600.0).mono().tracking(1.0),
        Type::Tag => TextStyle::new(10.0, 700.0).mono().tracking(0.8),
        Type::Mono => TextStyle::new(12.0, 400.0).mono(),
        Type::Control => TextStyle::new(12.0, 600.0),
    }
}

pub(crate) fn font(e: El, kind: Type) -> El {
    e.font(style(kind))
}

pub(crate) fn mono_span(span: Span) -> Span {
    span.mono().size(style(Type::Mono).size)
}

pub(crate) fn gap(e: El, gap: Space) -> El {
    e.gap(gap.pt())
}

pub(crate) fn inset(e: El, x: Space, y: Space) -> El {
    e.px(x.pt()).py(y.pt())
}

pub(crate) fn hang_left(e: El, by: Space) -> El {
    e.ml(-by.pt())
}

pub(crate) fn corner(e: El, radius: Radius) -> El {
    e.radius(radius.pt())
}

pub(crate) fn area(area: rune_kit::TextArea, kind: Type, x: Space, y: Space) -> rune_kit::TextArea {
    area.font(style(kind)).padding(x.pt(), y.pt())
}
