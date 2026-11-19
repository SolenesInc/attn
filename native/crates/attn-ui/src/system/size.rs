use super::Space;

/// How tall a control is. Its text inset belongs to its size, so words sit at one inset in
/// every field and button of a size.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Size {
    #[default]
    Regular,
    Compact,
}

impl Size {
    pub(crate) const fn height(self) -> f32 {
        match self {
            Size::Regular => 32.0,
            Size::Compact => 26.0,
        }
    }

    pub(crate) const fn inset(self) -> Space {
        match self {
            Size::Regular => Space::S12,
            Size::Compact => Space::S8,
        }
    }
}
