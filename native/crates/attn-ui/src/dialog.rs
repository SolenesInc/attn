use rune::{Color, El, Shadow, TextStyle, Tx, col, el, row, text};
use rune_kit::ButtonVariant;

use crate::frame::shadow;
use crate::system::{IconSize, Size, door};
use crate::{GlyphButton, Tokens};

/// How a dialog is framed, as the Tauri app's CSS draws that family of dialog: the backdrop
/// behind it, its surface, the header's insets and the rule under it, the title and subtitle
/// type, and the × that closes it.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct DialogFrame {
    pub backdrop: Backdrop,
    pub surface: Frame,
    pub glow: Option<Shadow>,
    /// `[top, right, bottom, left]`, as CSS `padding`.
    pub header: [f32; 4],
    pub rule: Option<Color>,
    pub title: TextStyle,
    pub subtitle: TextStyle,
    pub subtitle_ink: Color,
    pub subtitle_gap: f32,
    pub close: Option<Close>,
    /// Painted over the surface's fill, as CSS `background` layers: a vertical gradient from its
    /// first color to its second.
    pub sheen: Option<[Color; 2]>,
    /// Painted over the sheen: a gradient at an angle in radians, as CSS
    /// `linear-gradient(135deg, …)`, through its stops.
    pub wash: Option<(f32, [(f32, Color); 2])>,
}

/// A dialog's × button: a `Large` mark with 4 points round it, a compact control's height
/// square.
const CLOSE_SIDE: f32 = Size::Compact.height();

/// The × in a dialog's header: the glyph's size, its ink and the square it is pressed in.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Close {
    pub size: IconSize,
    pub ink: Color,
    pub side: f32,
}

impl DialogFrame {
    /// A sheet that lists or edits, with a ruled header: `.shortcuts-modal`,
    /// `.shortcut-editor-modal`.
    pub fn sheet(t: &Tokens) -> DialogFrame {
        DialogFrame {
            backdrop: Backdrop::modal(t),
            surface: Frame {
                fill: t.bg_app,
                edge: t.border,
                radius: 12.0,
                shadow: shadow(t, 24.0, 80.0, 0.8),
            },
            glow: Some(glow(t, 120.0, 0.04)),
            header: [16.0, 20.0, 16.0, 20.0],
            rule: Some(t.border_subtle),
            title: TextStyle::new(14.0, 600.0).tracking(-0.14),
            subtitle: TextStyle::new(12.0, 400.0),
            subtitle_ink: t.text_dimmed,
            subtitle_gap: 4.0,
            close: Some(Close {
                size: IconSize::Large,
                ink: t.text_dimmed,
                side: CLOSE_SIDE,
            }),
            sheen: None,
            wash: None,
        }
    }

    /// A question that Cancel and one action answer, with no ×: `.mp-dialog`.
    pub fn prompt(t: &Tokens) -> DialogFrame {
        DialogFrame {
            backdrop: Backdrop::overlay(t),
            surface: Frame {
                fill: t.bg_panel,
                edge: t.border_hover,
                radius: 12.0,
                shadow: shadow(t, 20.0, 60.0, 0.5),
            },
            glow: None,
            header: [22.0, 24.0, 24.0, 24.0],
            rule: None,
            title: TextStyle::new(21.0, 700.0),
            subtitle: TextStyle::new(13.0, 400.0),
            subtitle_ink: t.text_primary,
            subtitle_gap: 15.0,
            close: None,
            sheen: None,
            wash: None,
        }
    }
}

/// `box-shadow: 0 0 {blur}px color-mix(in srgb, var(--color-brand) {share}, transparent)`.
fn glow(t: &Tokens, blur: f32, share: f32) -> Shadow {
    Shadow {
        blur,
        color: t.brand.alpha(share),
        ..Shadow::default()
    }
}

enum Height {
    Fit(Option<f32>),
    Window(f32),
}

type Wrap = Box<dyn FnOnce(El) -> El>;

/// A modal dialog in a [`DialogFrame`]. Focus, dismissal and the inert background are Rune's
/// modal stack; whether it is open is the view's.
pub struct Dialog {
    key: String,
    title: String,
    frame: DialogFrame,
    subtitle: Option<String>,
    body: Vec<El>,
    width: f32,
    height: Height,
    outside: bool,
    header: Vec<El>,
    lead: Option<El>,
    close: Option<(Wrap, Wrap)>,
}

impl Dialog {
    pub fn new(key: impl Into<String>, title: impl Into<String>, frame: DialogFrame) -> Self {
        Dialog {
            key: key.into(),
            title: title.into(),
            frame,
            subtitle: None,
            body: Vec::new(),
            width: 480.0,
            height: Height::Fit(None),
            outside: true,
            header: Vec::new(),
            lead: None,
            close: None,
        }
    }

    /// A sentence under the title, in the frame's subtitle type; it also describes the dialog.
    pub fn subtitle(mut self, subtitle: impl Into<String>) -> Self {
        self.subtitle = Some(subtitle.into());
        self
    }

    pub fn child(mut self, body: El) -> Self {
        self.body.push(body);
        self
    }

    pub fn width(mut self, width: f32) -> Self {
        self.width = width;
        self
    }

    /// As tall as `share` of the window, as CSS `height: 84vh`; the body takes what the header
    /// leaves.
    pub fn window_height(mut self, share: f32) -> Self {
        self.height = Height::Window(share);
        self
    }

    /// As tall as its content up to `points`, as CSS `max-height`. Every dialog stops at the
    /// window too: past either, the body shrinks, so a part of it that scrolls takes the cut.
    pub fn max_height(mut self, points: f32) -> Self {
        self.height = Height::Fit(Some(points));
        self
    }

    pub fn click_outside(mut self, close: bool) -> Self {
        self.outside = close;
        self
    }

    /// Puts `e` in the header between the title and the ×.
    pub fn header(mut self, e: El) -> Self {
        self.header.push(e);
        self
    }

    /// Drawn in place of the title and subtitle, which still name the dialog.
    pub fn lead(mut self, e: El) -> Self {
        self.lead = Some(e);
        self
    }

    pub fn on_close<S: 'static>(mut self, f: impl Fn(&mut S) + Clone + 'static) -> Self {
        let button = f.clone();
        self.close = Some((
            Box::new(move |e| e.on_press(button)),
            Box::new(move |e| e.modal(true, f)),
        ));
        self
    }

    /// [`on_close`](Self::on_close) with the handler's `Tx`, for a close that moves focus or
    /// sends something before the dialog goes, or stays open.
    pub fn on_close_tx<S: 'static>(
        mut self,
        f: impl Fn(&mut S, &mut Tx) + Clone + 'static,
    ) -> Self {
        let button = f.clone();
        self.close = Some((
            Box::new(move |e| e.on_press_tx(button)),
            Box::new(move |e| e.modal_tx(true, f)),
        ));
        self
    }

    pub fn build(self, t: &Tokens, layer: &mut Vec<El>) {
        let Some((press, modal)) = self.close else {
            return;
        };
        let k = crate::kit(t);
        let f = self.frame;
        let subtitled = self.subtitle.is_some();
        let mut title = col().grow(1.0).min_w(0.0).gap(f.subtitle_gap).child(
            text(self.title.clone())
                .font(f.title)
                .color(t.text_primary)
                .wrap(),
        );
        if let Some(subtitle) = self.subtitle.clone() {
            title = title.child(text(subtitle).font(f.subtitle).color(f.subtitle_ink).wrap());
        }
        if let Some(lead) = self.lead {
            title = col().key("lead").grow(1.0).min_w(0.0).child(lead);
        }
        let [top, right, bottom, left] = f.header;
        let mut header = row()
            .key("header")
            .w_full()
            .pt(top)
            .pr(right)
            .pb(bottom)
            .pl(left)
            .gap(12.0)
            .child(title)
            .children(self.header);
        header = if subtitled {
            header.items_start()
        } else {
            header.items_center()
        };
        if let Some(rule) = f.rule {
            header = header
                .border_sides([0.0, 0.0, 1.0, 0.0], rule)
                .border_layout();
        }
        if let Some(close) = f.close {
            let dismiss = GlyphButton::new("×", "Close")
                .variant(ButtonVariant::GhostMuted)
                .font(TextStyle {
                    weight: 600.0,
                    ..door::mark(close.size)
                })
                .color(close.ink)
                .side(close.side)
                .build(k)
                .key("close");
            header = header.child(press(dismiss));
        }
        let body = col().key("body").w_full().children(self.body);
        let inside = col().w_full().clip_rounded(f.surface.radius - 1.0);
        let (panel, inside, body, layer_box) = match self.height {
            Height::Fit(cap) => (
                match cap {
                    Some(points) => col().max_h(points),
                    None => col(),
                }
                .shrink()
                .min_h(0.0),
                inside.shrink().min_h(0.0),
                body.shrink().min_h(0.0),
                col().padding(16.0),
            ),
            Height::Window(share) => (
                col().h_frac(share),
                inside.grow(1.0).min_h(0.0),
                body.grow(1.0).min_h(0.0),
                col().px(16.0),
            ),
        };
        let mut panel = f
            .surface
            .draw(panel)
            .key(format!("{}-dialog", self.key))
            .role("dialog")
            .label(self.title.clone())
            .description(self.subtitle.unwrap_or_default())
            .w_full()
            .max_w(self.width)
            .border_layout()
            .child(inside.child(header).child(body))
            .token_motion(k.motion.overlay);
        if let Some(glow) = f.glow {
            panel = panel.box_shadow(glow);
        }
        if let Some([top, bottom]) = f.sheen {
            panel = panel.gradient(top, bottom);
        }
        if let Some((angle, stops)) = f.wash {
            panel = panel.linear_gradient(angle, stops);
        }
        let panel = modal(panel).modal_click_outside(self.outside);
        layer.push(
            f.backdrop
                .draw(format!("{}-scrim", self.key))
                .token_motion(k.motion.overlay),
        );
        layer.push(
            layer_box
                .key(format!("{}-layer", self.key))
                .absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .center()
                .child(panel),
        );
    }
}

/// A surface's fill, edge, corners and shadow, as main drew a dialog before the blocks.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Frame {
    pub fill: Color,
    pub edge: Color,
    pub radius: f32,
    pub shadow: Shadow,
}

impl Frame {
    pub fn draw(self, surface: El) -> El {
        surface
            .fill(self.fill)
            .border(1.0, self.edge)
            .radius(self.radius)
            .box_shadow(self.shadow)
    }
}

/// What a dialog lays between itself and the window, as main drew it before the blocks.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Backdrop {
    pub fill: Color,
    pub blur: f32,
}

impl Backdrop {
    fn modal(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.shadow.alpha(0.7),
            blur: 4.0,
        }
    }

    fn overlay(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.bg_overlay,
            blur: 0.0,
        }
    }

    pub fn draw(self, key: impl Into<String>) -> El {
        el().key(key.into())
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .backdrop_blur(self.blur)
            .fill(self.fill)
            .a11y_hidden()
    }
}
