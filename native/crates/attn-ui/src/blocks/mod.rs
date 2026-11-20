//! The blocks a page is built from. A view returns blocks that know what they are, and the
//! parent of each block decides the space between it and its neighbours. View code names what
//! each thing is, never a gap, a font or a corner.

mod actions;
mod banner;
mod block;
mod choices;
mod columns;
mod dialog;
mod disclosure;
mod facts;
mod field;
pub mod inspector;
mod list;
mod measure;
mod menu;
mod notice;
mod page;
mod popover;
mod prose;
mod rail;
mod rhythm;
mod section;
mod setting_row;
mod sheet;

pub use actions::Actions;
pub use banner::{Banner, BannerTone};
pub use block::Block;
pub use choices::{Choices, swatches};
pub use columns::Columns;
pub use dialog::{Dialog, DialogSize, TOUR_DRAWING};
pub use disclosure::Disclosure;
pub use facts::Facts;
pub use field::Field;
pub use inspector::Inspector;
pub use list::List;
pub use menu::Menu;
pub use notice::{Notice, Refusal, Status, saved};
pub use page::{Head, Page};
pub use popover::Popover;
pub use prose::Prose;
pub use rail::Rail;
pub use rhythm::{Inline, Stack};
pub use section::Section;
pub use setting_row::SettingRow;
pub use sheet::Sheet;

/// `blocks` one under another, spaced by what they are, for a surface that is not a page.
pub(crate) fn stacked(blocks: Vec<Block>) -> rune::El {
    block::spaced(blocks, block::inner_gap, None)
}
