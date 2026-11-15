use attn_protocol::SessionDelegationRole;
use rune::{Color, El, Icon, TextStyle, icon, text};

macro_rules! stroked {
    ($name:literal, $body:literal) => {
        Icon {
            name: $name,
            view: 16.0,
            body: concat!(
                r#"<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">"#,
                $body,
                "</g>"
            ),
        }
    };
}

static CHAIN: Icon = stroked!(
    "role-chain",
    r#"<circle cx="4" cy="3" r="2"/><circle cx="4" cy="13" r="2"/><circle cx="12" cy="3" r="2"/><path d="M4 5v6m0-3h4a4 4 0 0 0 4-4"/>"#
);
static ICONS: [(&str, Icon); 9] = [
    (
        "search",
        stroked!(
            "role-search",
            r#"<circle cx="7" cy="7" r="4"/><path d="m10 10 4 4"/>"#
        ),
    ),
    (
        "diamond",
        stroked!("role-diamond", r#"<path d="m8 1 7 7-7 7-7-7Z"/>"#),
    ),
    (
        "code",
        stroked!(
            "role-code",
            r#"<path d="m5 4-4 4 4 4m6-8 4 4-4 4M9 2 7 14"/>"#
        ),
    ),
    (
        "arrow",
        stroked!("role-arrow", r#"<path d="M3 13 13 3M4 3h9v9"/>"#),
    ),
    (
        "list",
        stroked!(
            "role-list",
            r#"<path d="M6 4h8M6 8h8M6 12h8M2 4h1M2 8h1M2 12h1"/>"#
        ),
    ),
    (
        "bug",
        stroked!(
            "role-bug",
            r#"<rect x="4" y="5" width="8" height="9" rx="4"/><path d="m5 2 2 3m4-3-2 3M1 7h3m8 0h3M1 11h3m8 0h3M8 6v7"/>"#
        ),
    ),
    (
        "spark",
        stroked!(
            "role-spark",
            r#"<path d="m8 1 2 5 5 2-5 2-2 5-2-5-5-2 5-2Z"/>"#
        ),
    ),
    (
        "circle",
        stroked!("role-circle", r#"<circle cx="8" cy="8" r="6"/>"#),
    ),
    (
        "layers",
        stroked!(
            "role-layers",
            r#"<path d="M8 2 14 5 8 8 2 5Z M2 8l6 3 6-3M2 11l6 3 6-3"/>"#
        ),
    ),
];

/// How a role draws: the chain's mark for a session without one, the role's icon, or the
/// first letter of its name when attn has no icon by that name.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Mark {
    Chain,
    Icon(&'static str),
    Letter(String),
}

impl Mark {
    pub(crate) fn of(role: Option<&SessionDelegationRole>) -> Mark {
        let Some(role) = role else { return Mark::Chain };
        let named = role.icon.as_deref().filter(|i| !i.is_empty()).or_else(|| {
            Some(match role.builtin.as_deref()? {
                "pathfinder" => "search",
                "builder" => "code",
                "reviewer" => "list",
                "orchestrator" => "spark",
                "prototyper" => "layers",
                _ => return None,
            })
        });
        match named.and_then(|n| ICONS.iter().find(|(name, _)| *name == n)) {
            Some((name, _)) => Mark::Icon(name),
            None => Mark::Letter(
                role.name
                    .trim()
                    .chars()
                    .next()
                    .map_or("○".into(), |c| c.to_uppercase().collect()),
            ),
        }
    }

    pub(crate) fn width(&self, size: f32, font: TextStyle) -> f32 {
        match self {
            Mark::Letter(letter) => super::fit::text_w(letter, font),
            _ => size,
        }
    }

    pub(crate) fn el(&self, size: f32, font: TextStyle, color: Color) -> El {
        let glyph = match self {
            Mark::Chain => &CHAIN,
            Mark::Icon(name) => {
                &ICONS
                    .iter()
                    .find(|(n, _)| n == name)
                    .expect("a known icon")
                    .1
            }
            Mark::Letter(letter) => {
                return text(letter.clone()).font(font).color(color).single_line();
            }
        };
        icon(glyph, size).color(color)
    }
}
