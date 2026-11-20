//! The blocks a page is built from. A view returns blocks that know what they are, and the
//! parent of each block decides the space between it and its neighbours. View code names what
//! each thing is, never a gap, a font or a corner.

mod actions;
mod banner;
mod block;
mod choices;
mod disclosure;
mod facts;
mod field;
pub mod inspector;
mod list;
mod measure;
mod notice;
mod page;
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
pub use disclosure::Disclosure;
pub use facts::Facts;
pub use field::Field;
pub use inspector::Inspector;
pub use list::List;
pub use notice::{Notice, Refusal, Status, saved};
pub use page::{Head, Page};
pub use prose::Prose;
pub use rail::Rail;
pub use rhythm::{Inline, Stack};
pub use section::Section;
pub use setting_row::SettingRow;
pub use sheet::Sheet;
