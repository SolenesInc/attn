use rune::{El, col, row};
use rune_kit::ScrollBar;

use super::block::{Block, inner_gap, page_gap, spaced};
use crate::Tokens;
use crate::system::{Radius, Space, Type, door, words};

/// A panel beside the content, such as a drawer or a form: its title with controls beside it
/// and its close button, then its blocks in a column that scrolls, and a footer that stays in
/// view under them.
pub struct Sheet {
    key: String,
    title: String,
    aside: Vec<El>,
    close: Option<El>,
    head: Option<El>,
    top: Option<El>,
    fill: Option<El>,
    framed: bool,
    fits: Option<f32>,
    article: Option<(String, String)>,
    blocks: Vec<Block>,
    footer: Vec<Block>,
}

impl Sheet {
    /// `key` names the sheet, `{key}.header` its title line, `{key}.body` the column that
    /// scrolls and `{key}.actions` its footer.
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Sheet {
        Sheet {
            key: key.into(),
            title: title.into(),
            aside: Vec::new(),
            close: None,
            head: None,
            top: None,
            fill: None,
            framed: false,
            fits: None,
            article: None,
            blocks: Vec::new(),
            footer: Vec::new(),
        }
    }

    /// A control after the title, such as a count or a switch.
    pub fn aside(mut self, control: impl Into<El>) -> Self {
        self.aside.push(control.into());
        self
    }

    /// The button that closes the sheet, at the title line's far end.
    pub fn close(mut self, button: impl Into<El>) -> Self {
        self.close = Some(button.into());
        self
    }

    /// What stands in the title's place, such as the tabs between the lists a panel holds.
    /// The title still names the sheet for assistive tools.
    pub fn head(mut self, head: El) -> Self {
        self.head = Some(head);
        self
    }

    /// A body that lays itself out in place of the blocks, such as a list beside its
    /// inspector, each scrolling on its own.
    pub fn fill(mut self, body: El) -> Self {
        self.fill = Some(body);
        self
    }

    /// The sheet stands as tall as what it holds, up to `tallest` or the room what holds it
    /// gives, whichever is less, past which its body scrolls inside it: for a sheet that opens
    /// over the window, such as a popover, rather than filling a drawer.
    pub fn fits(mut self, tallest: f32) -> Self {
        self.fits = Some(tallest);
        self
    }

    /// The body is one thing to read, such as an ask: keyed `key` rather than `{key}.body`, and
    /// read as an article named `label`.
    pub fn article(mut self, key: impl Into<String>, label: impl Into<String>) -> Self {
        self.article = Some((key.into(), label.into()));
        self
    }

    /// The sheet lies on a panel of its own over the window, apart from what is under it.
    pub fn framed(mut self) -> Self {
        self.framed = true;
        self
    }

    /// What stays under the title while the body scrolls, such as a list's toolbar or a list to
    /// choose what the body shows.
    pub fn top(mut self, el: El) -> Self {
        self.top = Some(el);
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.blocks.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.blocks.extend(blocks);
        self
    }

    /// What stays in view under the body, such as Save and Cancel and what went wrong saving.
    pub fn footer(mut self, block: Block) -> Self {
        self.footer.push(block);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let key = self.key;
        let label = self.title.clone();
        let title = match self.head {
            Some(head) => head.grow(1.0).min_w(0.0),
            None => words(self.title, Type::PageTitle, t.text_primary)
                .role("heading")
                .label(label.clone())
                .truncate()
                .grow(1.0)
                .min_w(0.0),
        };
        let header = door::gap(row().w_full().items_center(), Space::S8)
            .key(format!("{key}.header"))
            .min_h(door::line_height(Type::PageTitle))
            .child(title)
            .children(self.aside)
            .children(self.close);
        let header = door::frame(header, Space::S16, Space::S16, Space::S12)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle);
        let top = self.top.map(|top| {
            door::frame(top, Space::S4, Space::S16, Space::S4)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        });
        let body = match self.fill {
            Some(body) => body.key(format!("{key}.body")).grow(1.0).min_h(0.0),
            None => {
                let blocks = door::frame(
                    spaced(self.blocks, page_gap, Some(t.border_subtle)),
                    Space::S16,
                    Space::S16,
                    Space::S24,
                );
                let body = match self.article {
                    Some((article, label)) => col().key(article).role("article").label(label),
                    None => col().key(format!("{key}.body")),
                };
                let body = body.w_full();
                let body = if self.fits.is_some() {
                    body.shrink().min_h(0.0)
                } else {
                    body.grow(1.0).min_h(0.0)
                };
                body.scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .child(blocks)
                .scroll_bar_styled(crate::scroll_bar(t))
            }
        };
        let footer = (!self.footer.is_empty()).then(|| {
            door::frame(
                spaced(self.footer, inner_gap, None),
                Space::S12,
                Space::S16,
                Space::S16,
            )
            .key(format!("{key}.actions"))
            .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
        });
        let sheet = col().key(key).label(label).w_full().min_h(0.0);
        let sheet = match self.fits {
            Some(tallest) => sheet.max_h(tallest).shrink(),
            None => sheet.h_full(),
        };
        let sheet = sheet
            .child(header)
            .children(top)
            .child(body)
            .children(footer);
        if self.framed {
            door::clip_corner(
                door::corner(sheet, Radius::Panel)
                    .fill(t.bg_panel)
                    .border(1.0, t.border),
                Radius::Panel,
            )
        } else {
            sheet
        }
    }
}
