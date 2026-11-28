//! The blocks a page is built from. A view returns blocks that know what they are, and the
//! parent of each block decides the space between it and its neighbours. View code names what
//! each thing is, never a gap, a font or a corner.

mod actions;
mod audio;
mod banner;
mod bar;
mod block;
mod board;
mod byline;
mod card;
mod cells;
mod chart;
mod choices;
mod columns;
mod data_table;
mod dialog;
mod disclosure;
mod drag_marks;
mod entry;
mod facts;
mod field;
mod figure;
mod floating_panel;
mod group;
mod header_piece;
mod id_labels;
pub mod inspector;
mod key_line;
mod lane;
mod list;
mod match_code;
mod measure;
mod menu;
mod miniature;
mod notice;
mod page;
mod pane;
mod pill;
mod place;
mod popover;
mod preedit;
mod prose;
mod rail;
mod reader;
mod remark;
mod rhythm;
mod screen;
mod section;
mod setting_row;
mod sheet;
mod shelf;
mod shelf_row;
mod surface;
mod table;
mod tabs;
mod terminal_bar;
mod terminal_notices;
mod terminal_status;
mod tile_frame;
mod tile_header;
mod tip;
mod toolbar;
mod trail;
mod tray;
mod window;

pub use actions::Actions;
pub use audio::{Audio, Playing};
pub use banner::{Banner, BannerTone};
pub use bar::Bar;
pub use block::Block;
pub use board::{Board, BoardCard};
pub use byline::Byline;
pub use card::{Card, Standing};
pub use cells::Cells;
pub use chart::Chart;
pub use choices::{Choices, swatches};
pub use columns::Columns;
pub use data_table::DataTable;
pub use dialog::{Dialog, DialogSize, TOUR_DRAWING};
pub use disclosure::Disclosure;
pub use drag_marks::{drag_ghost, drop_target};
pub use entry::{Entry, entries, rail};
pub use facts::Facts;
pub use field::Field;
pub use figure::Figure;
pub use floating_panel::FloatingPanel;
pub use group::Group;
pub use header_piece::HeaderPiece;
pub use id_labels::{IdLabel, IdMark, IdPlace, id_list, id_scrim, lantern, name_in_place};
pub use inspector::Inspector;
pub use key_line::{
    HintFit, Hints, KeyFit, KeyLine, key_choice, key_choice_width, key_hint, key_tab,
};
pub use lane::{Lane, board, lanes};
pub use list::List;
pub use match_code::MatchCode;
pub use menu::Menu;
pub use miniature::{Miniature, Overview};
pub use notice::{Notice, Refusal, Status, saved, show_refusal};
pub use page::{Head, Page};
pub use pane::Pane;
pub use pill::pill;
pub use place::{edged, here, here_tint, place, raised_here_tint, raised_place};
pub use popover::Popover;
pub use preedit::preedit;
pub use prose::Prose;
pub use rail::Rail;
pub use reader::{Reader, Room};
pub use remark::{Remark, Voice};
pub use rhythm::{Inline, Stack};
pub use screen::Screen;
pub use section::Section;
pub use setting_row::SettingRow;
pub use sheet::Sheet;
pub use shelf::{
    CountSide, MemberState, Place, Shelf, ShelfCount, ShelfEntry, ShelfMember, state_mark,
};
pub use shelf_row::{Laid, ShelfRow};

/// `blocks` one under another, spaced by what they are, for a surface that is not a page.
pub(crate) fn stacked(blocks: Vec<Block>) -> rune::El {
    block::spaced(blocks, block::inner_gap, None)
}
pub use surface::Surface;
pub use tabs::Tabs;
pub use terminal_bar::TerminalBar;
pub use terminal_notices::terminal_notices;
pub use terminal_status::terminal_status;
pub use tile_frame::{tile_area, tile_content};
pub use tile_header::{Settling, SettlingChip, TileHeader, Verdict, VerdictChip};
pub use tip::tip;
pub use toolbar::Toolbar;
pub use trail::Trail;
pub use tray::Tray;
pub use window::{Foot, WindowBar, at_foot, pointing_ring};

pub use measure::{DOCUMENT, DOCUMENT_EDGE, DOCUMENT_GUTTER, document_inset, document_inside};
pub use table::{Column, Empty, SubRow, Table, TableColumns, TableRow};
