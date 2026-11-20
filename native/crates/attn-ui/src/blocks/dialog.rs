use rune::{El, Tx, col, row};
use rune_kit::ScrollBar;

use super::actions::Actions;
use super::block::{Block, page_gap, spaced};
use super::measure::{PROMPT, READING};
use crate::system::{IconSize, icon_button};
use crate::system::{Said, Size, Space, Type, door, words};
use crate::{Backdrop, Frame, Tokens, glyph};

enum Height {
    Fit(Option<f32>),
    Window(f32),
    Fill(f32),
}

/// How big a dialog is, by what it holds. A dialog also stops at the window: past it, its body
/// shrinks and scrolls.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum DialogSize {
    /// A question and its answer: the lead at the reading measure inside the insets, as tall
    /// as it holds.
    #[default]
    Prompt,
    /// A form or a report a few blocks long: a pane's note beside its box, "pending_approval
    /// agent, not currently mounted" at 268.7 in `Small`, keeps one line; at most 760 tall.
    Form,
    /// A reference or an editor of many rows, 84% of the window tall: two columns of 340 hold
    /// a shortcut's name and its keys on one line.
    Sheet,
    /// A tour's step: its 552-wide drawing over its words, the body 480 tall whatever the step
    /// says (the drawing's 250, the title, three lines, a button and a line of keys), so the foot
    /// holds still from step to step.
    Tour,
    /// A surface of its own, such as Settings: its navigation, [`Dialog::NAVIGATION`] wide,
    /// beside the widest page, filling the window down to 880 tall, its lead quiet on the
    /// title's line so the page keeps the height.
    Workspace,
    /// A board of cards, such as the desktop overview's desktops in miniature: three cards of
    /// about 276 side by side, as tall as they hold up to 800, the overview's size before the
    /// design system.
    Board,
}

/// A tour's drawing: the width its scenes are painted at.
pub const TOUR_DRAWING: f32 = 552.0;

impl DialogSize {
    fn width(self) -> f32 {
        match self {
            DialogSize::Prompt => PROMPT,
            DialogSize::Form => 620.0,
            DialogSize::Sheet => 760.0,
            DialogSize::Tour => TOUR_DRAWING + 2.0 * Space::S24.pt(),
            DialogSize::Workspace => Dialog::NAVIGATION + super::page::Page::WIDEST,
            DialogSize::Board => 880.0,
        }
    }

    fn height(self) -> Height {
        match self {
            DialogSize::Prompt | DialogSize::Tour => Height::Fit(None),
            DialogSize::Form => Height::Fit(Some(760.0)),
            DialogSize::Board => Height::Fit(Some(800.0)),
            DialogSize::Sheet => Height::Window(0.84),
            DialogSize::Workspace => Height::Fill(880.0),
        }
    }

    fn body_height(self) -> Option<f32> {
        (self == DialogSize::Tour).then_some(480.0)
    }
}

type Wrap = Box<dyn FnOnce(El) -> El>;

/// A dialog: its title and what it is for, a body of blocks, and a footer of actions. It is
/// modal: focus, dismissal and the inert window behind are Rune's modal stack, and whether it
/// is open is the view's. A dialog with actions closes with its Cancel, Escape or a click
/// outside; one without, or one whose actions do not close it, draws a × that closes it.
pub struct Dialog {
    key: String,
    title: Said,
    lead: Option<Said>,
    aside: Vec<El>,
    body: Vec<Block>,
    list: Option<El>,
    top: Option<El>,
    actions: Option<Actions>,
    size: DialogSize,
    named: Option<(String, String)>,
    outside: bool,
    close: Option<(Wrap, Wrap)>,
    keys: Option<Wrap>,
    close_button: bool,
    autofocus: bool,
    over: Option<El>,
}

impl Dialog {
    /// `key` names the dialog: its surface is `{key}-dialog`, its backdrop `{key}-scrim`, the
    /// body that scrolls `{key}.body`.
    pub fn new(key: impl Into<String>, title: impl Into<Said>) -> Self {
        Dialog {
            key: key.into(),
            title: title.into(),
            lead: None,
            aside: Vec::new(),
            body: Vec::new(),
            list: None,
            top: None,
            actions: None,
            size: DialogSize::Prompt,
            named: None,
            outside: true,
            close: None,
            keys: None,
            close_button: false,
            autofocus: false,
            over: None,
        }
    }

    /// How wide a [`DialogSize::Workspace`] dialog's navigation stands: "Files and locations"
    /// on one line in `Body` inside a list row's insets.
    pub const NAVIGATION: f32 = 258.0;

    /// The focus rests on the dialog itself when it opens, so the first Tab goes to its first
    /// control.
    pub fn autofocus(mut self) -> Self {
        self.autofocus = true;
        self
    }

    /// What stands over the dialog's surface, such as the toast that says what an action did.
    pub fn over(mut self, el: Option<El>) -> Self {
        self.over = el;
        self
    }

    /// What the dialog asks or is for, under its title; it also describes the dialog. Text
    /// between backticks is set in `Mono`.
    pub fn lead(mut self, lead: impl Into<Said>) -> Self {
        let lead = lead.into();
        self.lead = (!lead.is_empty()).then_some(lead);
        self
    }

    /// A control beside the title, such as a button that opens an editor of what the dialog
    /// lists.
    pub fn aside(mut self, control: impl Into<El>) -> Self {
        self.aside.push(control.into());
        self
    }

    /// What stays under the head while the body scrolls, such as a field that filters it.
    pub fn top(mut self, top: El) -> Self {
        self.top = Some(top);
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.body.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.body.extend(blocks);
        self
    }

    /// A body that scrolls between a ruled head and foot, such as a long list, in place of
    /// the blocks. It takes the dialog's whole width, so it sets its own insets.
    pub fn list(mut self, list: El) -> Self {
        self.list = Some(list);
        self
    }

    /// Draws the × beside the title though the dialog has actions, for a dialog none of whose
    /// actions closes it, such as a tour's Back and Next.
    pub fn close_button(mut self) -> Self {
        self.close_button = true;
        self
    }

    pub fn actions(mut self, actions: Actions) -> Self {
        self.actions = Some(actions);
        self
    }

    /// Keys the surface `surface` and the backdrop `scrim` in place of `{key}-dialog` and
    /// `{key}-scrim`, for a dialog whose keys tests and scripts found before it was a Dialog.
    pub fn surface_keys(mut self, surface: impl Into<String>, scrim: impl Into<String>) -> Self {
        self.named = Some((surface.into(), scrim.into()));
        self
    }

    pub fn size(mut self, size: DialogSize) -> Self {
        self.size = size;
        self
    }

    /// Keys the dialog's own do not cover, heard anywhere in it first, such as a tour's arrows
    /// that step it; `f` says whether it took the key.
    pub fn on_key<S: 'static>(
        mut self,
        f: impl Fn(&mut S, &rune::KeyEvent, &mut Tx) -> bool + 'static,
    ) -> Self {
        self.keys = Some(Box::new(move |e| e.on_key(f)));
        self
    }

    pub fn click_outside(mut self, close: bool) -> Self {
        self.outside = close;
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
        let (surface_key, scrim_key) = self.named.clone().unwrap_or_else(|| {
            (
                format!("{}-dialog", self.key),
                format!("{}-scrim", self.key),
            )
        });
        let Some((press, modal)) = self.close else {
            return;
        };
        let k = crate::kit(t);
        let closer = (self.actions.is_none() || self.close_button).then(|| {
            press(
                icon_button("close", "Close", glyph::TIMES, IconSize::Large)
                    .build(t)
                    .into(),
            )
        });
        let ruled = self.list.is_some();
        let bodied = ruled || !self.body.is_empty();
        let footed = self.actions.is_some();
        let head = door::frame(
            head(
                &self.title,
                self.lead.clone(),
                self.size == DialogSize::Workspace,
                self.aside,
                closer,
                t,
            ),
            Space::S24,
            Space::S24,
            match (ruled, bodied || footed) {
                (true, _) => Space::S16,
                (false, true) => Space::S0,
                (false, false) => Space::S24,
            },
        );
        let body = match self.list {
            Some(list) => Some(door::rule_above(
                col().w_full().child(list.w_full().grow(1.0).min_h(0.0)),
                t.border_subtle,
            )),
            None => bodied.then(|| {
                door::frame(
                    spaced(self.body, page_gap, Some(t.border_subtle)),
                    Space::S16,
                    Space::S24,
                    if footed { Space::S0 } else { Space::S24 },
                )
                .key(format!("{}.body", self.key))
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .scroll_bar_styled(crate::scroll_bar(t))
            }),
        };
        let foot = self.actions.map(|actions| {
            let line = door::frame(
                col().key("actions").w_full().child(actions.build().el),
                if ruled { Space::S16 } else { Space::S24 },
                Space::S24,
                Space::S24,
            );
            if ruled {
                door::rule_above(line, t.border_subtle)
            } else {
                line
            }
        });
        let body = match self.size.body_height() {
            Some(points) => body.map(|b| b.h(points)),
            None => body,
        };
        let top = self
            .top
            .map(|top| door::frame(top, Space::S12, Space::S24, Space::S0));
        let inside = col().w_full().child(head).children(top);
        let (panel, inside, body, layer_box) = match self.size.height() {
            Height::Fit(cap) => (
                match cap {
                    Some(points) => col().max_h(points),
                    None => col(),
                }
                .shrink()
                .min_h(0.0),
                inside.shrink().min_h(0.0),
                body.map(|b| b.shrink().min_h(0.0)),
                door::inset(col(), Space::S16, Space::S16),
            ),
            Height::Window(share) => (
                col().h_frac(share),
                inside.grow(1.0).min_h(0.0),
                body.map(|b| b.grow(1.0).min_h(0.0)),
                door::inset(col(), Space::S16, Space::S0),
            ),
            Height::Fill(tallest) => (
                col().h_full().max_h(tallest),
                inside.grow(1.0).min_h(0.0),
                body.map(|b| b.grow(1.0).min_h(0.0)),
                door::inset(col(), Space::S16, Space::S16),
            ),
        };
        let panel = Frame::dialog(t)
            .draw_clipped(panel)
            .key(surface_key)
            .role("dialog")
            .label(self.title.text())
            .description(self.lead.map(|lead| lead.text()).unwrap_or_default())
            .w_full()
            .max_w(self.size.width())
            .child(inside.children(body).children(foot))
            .children(self.over)
            .token_motion(k.motion.overlay);
        let panel = if self.autofocus {
            panel.autofocus()
        } else {
            panel
        };
        let panel = match self.keys {
            Some(keys) => keys(panel),
            None => panel,
        };
        let panel = modal(panel).modal_click_outside(self.outside);
        layer.push(
            Backdrop::modal(t)
                .draw(scrim_key)
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

/// The dialog's head: its title and lead, then what goes beside the title, each centred on the
/// title's first line.
fn head(
    title: &Said,
    lead: Option<Said>,
    lead_beside: bool,
    aside: Vec<El>,
    closer: Option<El>,
    t: &Tokens,
) -> El {
    let band = if !aside.is_empty() {
        Some(Size::Regular)
    } else if closer.is_some() {
        Some(Size::Compact)
    } else {
        None
    };
    let title = words(title, Type::Heading, t.text_primary)
        .key("title")
        .role("heading")
        .wrap();
    let title = match band {
        Some(size) => door::on_band(title, Type::Heading, size),
        None => title,
    };
    let said = if lead_beside {
        door::gap(row().grow(1.0).min_w(0.0).items_center(), Space::S16)
            .child(title)
            .children(lead.map(|lead| {
                words(lead, Type::Body, t.text_tertiary)
                    .key("lead")
                    .single_line()
                    .truncate()
                    .shrink()
                    .min_w(0.0)
            }))
    } else {
        door::gap(col().grow(1.0).min_w(0.0), Space::S8)
            .child(title)
            .children(lead.map(|lead| {
                words(lead, Type::Body, t.text_secondary)
                    .key("lead")
                    .wrap()
                    .max_w(READING)
            }))
    };
    let beside = door::gap(row().items_center(), Space::S8)
        .children(aside)
        .children(closer);
    let beside = match band {
        Some(size) => beside.h(size.height()),
        None => beside,
    };
    door::gap(row().key("header").w_full().items_start(), Space::S16)
        .child(said)
        .child(beside)
}
