//! The blocks a page is built from. A view returns blocks that know what they are, and the
//! parent of each block decides the space between it and its neighbours. View code names what
//! each thing is, never a gap, a font or a corner.

mod actions;
mod banner;
mod bar;
mod block;
mod byline;
mod card;
mod choices;
mod disclosure;
mod entry;
mod field;
mod form;
mod lane;
mod list_row;
mod measure;
mod menu;
mod note;
mod notice;
mod page;
mod pane;
mod prose;
mod reader;
mod rhythm;
mod section;
mod setting_row;
mod surface;
mod trail;

pub use actions::Actions;
pub use banner::{Banner, BannerTone};
pub use bar::Bar;
pub use block::Block;
pub use byline::Byline;
pub use card::{Card, Standing};
pub use choices::{Choices, swatches};
pub use disclosure::Disclosure;
pub use entry::{Entry, entries, rail};
pub use field::Field;
pub use form::Form;
pub use lane::{Lane, board, lanes};
pub use list_row::{ListRow, rows, strip};
pub use menu::Menu;
pub use note::Note;
pub use notice::{Notice, Refusal, Status, saved};
pub use page::{Head, Page};
pub use pane::Pane;
pub use prose::Prose;
pub use reader::{Reader, Room};
pub use rhythm::{Inline, Stack};
pub use section::Section;
pub use setting_row::SettingRow;
pub use surface::Surface;
pub use trail::Trail;

pub use measure::DOCUMENT;
