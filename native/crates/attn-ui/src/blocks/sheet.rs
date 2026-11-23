use rune::{El, col, row};

use super::block::{Block, Opens, Room, bounded, inner_gap, page_gap, scrolling, spaced};
use super::notice::Notice;
use crate::Tokens;
use crate::system::{Radius, Said, Space, Type, door, words};

const NOTICES_GAP: Space = Space::S12;
const NOTICES_INSET_Y: Space = Space::S12;

/// A panel beside the content, such as a drawer or a form: its title with controls beside it
/// and its close button, then what went wrong that must stay in view, then its blocks in a
/// column that scrolls, and a footer that stays in view under them.
pub struct Sheet {
    key: String,
    title: Said,
    badge: Option<El>,
    aside: Vec<El>,
    close: Option<El>,
    head: Option<El>,
    top: Option<El>,
    notices: Vec<Notice>,
    fill: Option<El>,
    framed: bool,
    fits: Option<f32>,
    room: f32,
    opens: Opens,
    least: f32,
    article: Option<(String, String)>,
    measure: Option<(f32, f32)>,
    body_key: Option<String>,
    blocks: Vec<Block>,
    footer: Vec<Block>,
}

impl Sheet {
    /// `key` names the sheet, `{key}.header` its title line, `{key}.notices` what stays in view
    /// over its body, `{key}.body` the column that scrolls and `{key}.actions` its footer.
    pub fn new(key: impl Into<String>, title: impl Into<Said>) -> Sheet {
        Sheet {
            key: key.into(),
            title: title.into(),
            badge: None,
            aside: Vec::new(),
            close: None,
            head: None,
            top: None,
            notices: Vec::new(),
            fill: None,
            framed: false,
            fits: None,
            room: 0.0,
            opens: Opens::AtStart,
            least: 0.0,
            article: None,
            measure: None,
            body_key: None,
            blocks: Vec::new(),
            footer: Vec::new(),
        }
    }

    /// A control after the title, such as a count or a switch.
    /// A mark right after the title that sums the sheet up, such as how many are open.
    pub fn badge(mut self, badge: impl Into<El>) -> Self {
        self.badge = Some(badge.into());
        self
    }

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
    /// The body keeps at least `points` of the sheet's height, so the top band and the notices
    /// give way first when the sheet is short, as the list of asks gives way to the ask it shows.
    pub fn keeps_room(mut self, points: f32) -> Self {
        self.room = points;
        self
    }

    /// The body never goes shorter than `points`, however short the sheet: past that, the
    /// notices give way, scrolling inside their band, so the person still sees what they are
    /// about, as a draft a save conflict would overwrite.
    pub fn keeps_at_least(mut self, points: f32) -> Self {
        self.least = points;
        self
    }

    pub fn fits(mut self, tallest: f32) -> Self {
        self.fits = Some(tallest);
        self
    }

    /// The body that scrolls is keyed `key` rather than `{key}.body`, as a feature whose key
    /// names the part that scrolls.
    pub fn body_key(mut self, key: impl Into<String>) -> Self {
        self.body_key = Some(key.into());
        self
    }

    /// The body opens at its end and stays there as more arrives, as messages whose newest is
    /// last.
    pub fn opens_at_end(mut self) -> Self {
        self.opens = Opens::AtEnd;
        self
    }

    /// The body is one thing to read, such as an ask: keyed `key` rather than `{key}.body`, and
    /// read as an article named `label`.
    pub fn article(mut self, key: impl Into<String>, label: impl Into<String>) -> Self {
        self.article = Some((key.into(), label.into()));
        self
    }

    /// The body's blocks run at most `text` wide, and its wide blocks ([`Block::wide`]) at
    /// most `wide`, centred however wide the sheet: a column read at a measure, such as an ask,
    /// whose pictures take more room than its lines.
    pub fn measure(mut self, text: f32, wide: f32) -> Self {
        self.measure = Some((text, wide));
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

    /// What went wrong that the person must see wherever the body is scrolled, with what
    /// recovers from it, such as a save that failed: under the title line, over the top and
    /// the body. They give way to the room the body keeps ([`Self::keeps_room`]): their
    /// sentences scroll, down to their first line. Their actions stay whole, before that room:
    /// the body gives up room only once the notices are at their first lines, and only down to
    /// what it keeps at least ([`Self::keeps_at_least`]). Past that, the band scrolls.
    pub fn notices(mut self, notices: impl IntoIterator<Item = Notice>) -> Self {
        self.notices.extend(notices);
        self
    }

    /// Buttons that act on the whole sheet, such as adding to it or clearing it, on a line under
    /// the title and any notices that wraps when the sheet is narrow: Tab reaches them after the
    /// title's controls and the notices' actions, before the body.
    pub fn top_actions(self, actions: Block) -> Self {
        self.top(actions.el)
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
        let label = self.title.text();
        let title = match self.head {
            Some(head) => head.grow(1.0).min_w(0.0),
            None => words(self.title, Type::PageTitle, t.text_primary)
                .role("heading")
                .label(label.clone())
                .truncate()
                .min_w(0.0),
        };
        let title = match self.badge {
            Some(badge) => door::gap(row().items_center(), Space::S8)
                .grow(1.0)
                .min_w(0.0)
                .child(title.shrink())
                .child(badge),
            None => title.grow(1.0),
        };
        let header = door::gap(row().w_full().items_center(), Space::S8)
            .key(format!("{key}.header"))
            .min_h(door::line_height(Type::PageTitle))
            .child(title)
            .children(self.aside)
            .children(self.close);
        let header = door::frame(header, Space::S16, Space::S16, Space::S12)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle);
        let room_key = format!("{key}.room");
        let top_key = format!("{key}.top");
        let top_height = self
            .top
            .as_ref()
            .and_then(|_| rune::memo::layout_size(&top_key))
            .map_or(0.0, |[_, h]| h);
        let top = self.top.map(|top| {
            door::frame(top, Space::S4, Space::S16, Space::S4)
                .key(top_key)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
                .shrink_first()
        });
        let spare =
            rune::memo::layout_size(&room_key).map(|[_, h]| (h - top_height - self.least).max(0.0));
        let notices = (!self.notices.is_empty()).then(|| {
            let floor = self.notices.iter().map(Notice::floor).sum::<f32>()
                + NOTICES_GAP.pt() * (self.notices.len() - 1) as f32
                + 2.0 * NOTICES_INSET_Y.pt();
            let notices = door::gap(col().w_full(), NOTICES_GAP)
                .children(self.notices.into_iter().map(|notice| notice.pinned(t)));
            let band = door::frame(notices, NOTICES_INSET_Y, Space::S16, NOTICES_INSET_Y)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle);
            match spare.filter(|spare| *spare < floor) {
                Some(spare) => scrolling(
                    col()
                        .key(format!("{key}.notices"))
                        .w_full()
                        .child(band.h(floor)),
                    Opens::AtStart,
                    t,
                )
                .h(spare),
                None => band
                    .key(format!("{key}.notices"))
                    .shrink_first()
                    .min_h(floor),
            }
        });
        let body = match self.fill {
            Some(body) => body.key(format!("{key}.body")).grow(1.0).min_h(0.0),
            None => {
                let blocks = match self.measure {
                    Some((text, wide)) => self
                        .blocks
                        .into_iter()
                        .map(|block| block.measured(text, wide))
                        .collect(),
                    None => self.blocks,
                };
                let blocks = spaced(blocks, page_gap, Some(t.border_subtle));
                let blocks = door::frame(blocks, Space::S16, Space::S16, Space::S24);
                let body = match self.article {
                    Some((article, label)) => col().key(article).role("article").label(label),
                    None => col().key(
                        self.body_key
                            .clone()
                            .unwrap_or_else(|| format!("{key}.body")),
                    ),
                };
                let body = scrolling(body.w_full().child(blocks), self.opens, t);
                if self.fits.is_some() {
                    body.shrink().min_h(self.room)
                } else {
                    body.grow_from(1.0, self.room).shrink().min_h(self.least)
                }
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
        let room = col()
            .key(room_key)
            .w_full()
            .grow(1.0)
            .shrink()
            .min_h(0.0)
            .children(notices)
            .children(top)
            .child(body);
        let fits = match self.fits {
            Some(_) => Room::Fit,
            None => Room::Fill,
        };
        let sheet = bounded(header, Some(room), footer, fits)
            .key(key)
            .label(label);
        let sheet = match self.fits {
            Some(tallest) => sheet.max_h(tallest),
            None => sheet.h_full(),
        };
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
