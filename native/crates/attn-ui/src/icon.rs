//! The Tauri app's outlined icons, drawn by `rune::icon` in their element's colour. Each body is
//! the SVG the Tauri component draws, with the stroke its CSS gives it.

use rune::Icon;

macro_rules! outline {
    ($name:ident, $view:expr, $width:expr, $body:expr) => {
        pub static $name: Icon = Icon {
            name: concat!("attn-", stringify!($name)),
            view: $view,
            body: concat!(
                r#"<g fill="none" stroke="currentColor" stroke-width=""#,
                $width,
                r#"" stroke-linecap="round" stroke-linejoin="round">"#,
                $body,
                "</g>"
            ),
        };
    };
}

outline!(
    RESIZE_GRIP,
    10.0,
    "1",
    r#"<path d="M9 3 3 9"/><path d="M9 6.5 6.5 9"/>"#
);
outline!(CHEVRON_DOWN, 10.0, "1.5", r#"<path d="M2 3.5l3 3 3-3"/>"#);
outline!(
    GARDEN,
    16.0,
    "1.4",
    r#"<path d="M8 14V7.4"/><path d="M8 7.4C8 5.3 6.4 3.6 4.2 3.6c0 2.1 1.6 3.8 3.8 3.8Z"/><path d="M8 7.4c0-2.1 1.6-3.8 3.8-3.8 0 2.1-1.6 3.8-3.8 3.8Z"/>"#
);
outline!(
    LEDGER,
    16.0,
    "1.4",
    r#"<rect x="1.8" y="3" width="12.4" height="10" rx="1.6"/><path d="m4.8 6.9 1.9 1.9-1.9 1.9"/><path d="M8.8 10.7h2.6"/>"#
);
outline!(
    AUTOMATIONS,
    16.0,
    "1.4",
    r#"<path d="M8 1.6 9.6 5l3.6.6-2.6 2.6.6 3.6-3.2-1.7-3.2 1.7.6-3.6L2.8 5.6 6.4 5 8 1.6Z"/><circle cx="8" cy="8" r="1.3" fill="currentColor" stroke="none"/>"#
);
outline!(
    COMMANDS,
    16.0,
    "1.4",
    r#"<path d="M9.5 1.5 3 9h4l-.5 5.5L13 7H9z"/>"#
);
outline!(
    QUEUE_FLOW,
    16.0,
    "1.4",
    r#"<path d="M6 3h8M6 8h8M6 13h8"/><path d="m1.5 6 2 2-2 2"/><path d="M2.5 3h.01M2.5 13h.01"/>"#
);
outline!(
    DESKTOP_FLOW,
    16.0,
    "1.4",
    r#"<rect x="2" y="2" width="12" height="9" rx="1.5"/><path d="M8 11v3M5.5 14h5"/>"#
);
outline!(COLLAPSE, 16.0, "1.6", r#"<path d="M10 3 5 8l5 5"/>"#);
outline!(EXPAND, 16.0, "1.6", r#"<path d="M6 3l5 5-5 5"/>"#);
outline!(
    BELL,
    16.0,
    "1.4",
    r#"<path d="M8 2.2c-2.1 0-3.5 1.6-3.5 3.7 0 2.6-.7 3.6-1.4 4.3-.3.3-.1.9.4.9h9c.5 0 .7-.6.4-.9-.7-.7-1.4-1.7-1.4-4.3 0-2.1-1.4-3.7-3.5-3.7Z"/><path d="M6.6 12.6a1.5 1.5 0 0 0 2.8 0"/>"#
);
outline!(
    ASKS,
    16.0,
    "1.4",
    r#"<path d="M2.5 4A1.5 1.5 0 0 1 4 2.5h8A1.5 1.5 0 0 1 13.5 4v6a1.5 1.5 0 0 1-1.5 1.5H7l-3 2.5v-2.5A1.5 1.5 0 0 1 2.5 10Z"/><path d="M6.6 5.6a1.5 1.5 0 1 1 2 1.4c-.4.2-.6.5-.6.9v.2M8 9.4v.01"/>"#
);
outline!(
    HOME,
    16.0,
    "1.4",
    r#"<path d="M2.5 7.2 8 2.8l5.5 4.4v5.8a.5.5 0 0 1-.5.5H9.5V9H6.5v4.5H3a.5.5 0 0 1-.5-.5Z"/>"#
);
outline!(
    SETTINGS,
    24.0,
    "2",
    r#"<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>"#
);
outline!(
    REFRESH,
    24.0,
    "2.5",
    r#"<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>"#
);
outline!(
    PENCIL,
    16.0,
    "1.3",
    r#"<path d="m10.5 2.5 3 3-7.5 7.5-4 1 1-4zM9 4l3 3"/>"#
);
outline!(
    TRASH,
    16.0,
    "1.3",
    r#"<path d="M2 4h12M6 4V2h4v2M4 4l.5 10h7L12 4M6.5 7v4M9.5 7v4"/>"#
);
outline!(
    CONTEXT,
    24.0,
    "1.6",
    r#"<path d="M6 3.5h9l3 3V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z"/><path d="M15 3.5V7h3M8 11h7M8 14.5h7M8 18h4"/>"#
);
outline!(
    ATTENTION,
    24.0,
    "1.6",
    r#"<path d="M12 3.5a6 6 0 0 0-6 6v3.2L4.5 16h15L18 12.7V9.5a6 6 0 0 0-6-6Z"/><path d="M9.5 19a2.8 2.8 0 0 0 5 0"/>"#
);
outline!(
    KEYBOARD,
    24.0,
    "1.6",
    r#"<rect x="3" y="6" width="18" height="12" rx="2"/><path d="M7 10h.01M11 10h.01M15 10h.01M8 13.5h8"/>"#
);
outline!(
    BOARD,
    16.0,
    "1.4",
    r#"<rect x="2.5" y="2.5" width="3" height="11" rx="0.8"/><rect x="6.5" y="2.5" width="3" height="7" rx="0.8"/><rect x="10.5" y="2.5" width="3" height="9" rx="0.8"/>"#
);
outline!(
    NOTEBOOK,
    16.0,
    "1.4",
    r#"<path d="M4 2.5h7.5a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4Z"/><path d="M4 2.5a1.5 1.5 0 0 0-1.5 1.5v8A1.5 1.5 0 0 0 4 13.5"/><path d="M6 5.5h4M6 8h4" stroke-width="1.3"/>"#
);
outline!(
    MARKDOWN,
    16.0,
    "1.3",
    r#"<rect x="1.5" y="3" width="13" height="10" rx="1.5"/><path d="M3.6 10.4V5.6l2 2.4 2-2.4v4.8"/><path d="M10.6 5.6v4.2M9 8.4l1.6 1.6 1.6-1.6"/>"#
);
outline!(
    BROWSER,
    16.0,
    "1.3",
    r#"<circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c-2 2-2 10 0 12M8 2c2 2 2 10 0 12"/>"#
);
outline!(
    EDITOR,
    16.0,
    "1.4",
    r#"<path d="M3 2.5h7.5v2"/><path d="M8.5 3h4.5v4.5"/><path d="m7 9 6-6"/><path d="M13 9.5v3a1 1 0 0 1-1 1H3.5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h3"/>"#
);
outline!(
    RECENT,
    16.0,
    "1.6",
    r#"<circle cx="8" cy="8" r="6"/><path d="M8 4.8V8l2.2 1.4"/>"#
);
outline!(
    FOLDER,
    16.0,
    "1.6",
    r#"<path d="M2 4.5h4l1.4 1.6H14v6.4H2z"/>"#
);
outline!(
    REPO,
    16.0,
    "1.6",
    r#"<circle cx="4.5" cy="4" r="1.6"/><circle cx="4.5" cy="12" r="1.6"/><circle cx="11.5" cy="6" r="1.6"/><path d="M4.5 5.6v4.8M11.5 7.6c0 2-2.5 2.2-7 3"/>"#
);
outline!(PLUS, 16.0, "1.6", r#"<path d="M8 3v10M3 8h10"/>"#);
outline!(
    MAIN_BRANCH,
    16.0,
    "1.6",
    r#"<circle cx="8" cy="8" r="2.6"/><path d="M8 2v3.4M8 10.6V14"/>"#
);
outline!(
    BRANCH,
    16.0,
    "1.6",
    r#"<circle cx="4.5" cy="4" r="1.6"/><circle cx="11.5" cy="12" r="1.6"/><path d="M4.5 5.6c0 4 7 2.4 7 4.8"/>"#
);
outline!(
    MACHINE,
    16.0,
    "1.6",
    r#"<rect x="2" y="3" width="12" height="8" rx="1.2"/><path d="M6 13.5h4M8 11v2.5"/>"#
);
outline!(
    ELSEWHERE,
    16.0,
    "1.6",
    r#"<circle cx="7" cy="7" r="4"/><path d="M10 10l3.5 3.5"/>"#
);
outline!(
    DELETE,
    16.0,
    "1.6",
    r#"<path d="M3 5h10M6 5V3.5h4V5M5 5l.6 8h4.8L11 5"/>"#
);
outline!(
    FRAME_OUT,
    12.0,
    "1.25",
    r#"<path d="M4.5 1.5H1.5V4.5M7.5 10.5H10.5V7.5"/>"#
);
outline!(
    FRAME_IN,
    12.0,
    "1.25",
    r#"<path d="M1.5 4.5H4.5V1.5M10.5 7.5H7.5V10.5"/>"#
);
outline!(
    FOCUS_AGENT,
    16.0,
    "1.4",
    r#"<path d="M6 2.5H2.5V6M10 2.5h3.5V6M6 13.5H2.5V10M10 13.5h3.5V10"/>"#
);
outline!(
    UNFOCUS,
    16.0,
    "1.4",
    r#"<path d="M2.5 6H6V2.5M13.5 6H10V2.5M2.5 10H6v3.5M13.5 10H10v3.5"/>"#
);
outline!(
    POP_OUT,
    16.0,
    "1.3",
    r#"<path d="M9.5 2.5h4v4M13.5 2.5L8 8M6.5 3.5h-4v10h10v-4"/>"#
);
outline!(
    BRING_BACK,
    16.0,
    "1.3",
    r#"<path d="M6.5 13.5h-4v-4M2.5 13.5L8 8M9.5 2.5h4v10h-10"/>"#
);
outline!(
    FOCUS_DOCUMENT,
    16.0,
    "1.5",
    r#"<path d="M2.75 6V2.75H6M10 2.75h3.25V6M13.25 10v3.25H10M6 13.25H2.75V10"/>"#
);
outline!(
    REVIEW_NOTES,
    16.0,
    "1.5",
    r#"<path d="M3.25 3.25h9.5v7H7l-3.75 2.5v-9.5Z"/>"#
);
outline!(
    ANNOTATION_DELETE,
    24.0,
    "2",
    r#"<path d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/>"#
);
outline!(
    ANNOTATION_COMMENT,
    24.0,
    "2",
    r#"<path d="M7 8h10M7 12h4m1 8l-4-4H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-3l-4 4z"/>"#
);
outline!(
    ANNOTATION_CANCEL,
    24.0,
    "2",
    r#"<path d="M6 18L18 6M6 6l12 12"/>"#
);
outline!(
    ANNOTATION_LABEL,
    24.0,
    "2",
    r#"<path d="M13 10V3L4 14h7v7l9-11h-7z"/>"#
);
outline!(
    CREW_SUN,
    20.0,
    "1.3",
    r#"<path d="M10 13H8.04M10 13h1.96" opacity="0.45"/><path d="M5.24 11.45 4.13 11.09M7.06 8.95 6.37 8.01M10 8V6.83M12.94 8.95l.69-.94M14.76 11.45l1.11-.36"/><circle cx="10" cy="13" r="3.4" fill="currentColor" stroke="none"/>"#
);

/// The characters the Tauri app draws as text that no bundled font carries, drawn as that
/// character looks.
macro_rules! mark {
    ($name:ident, $body:expr) => {
        pub static $name: Icon = Icon {
            name: concat!("attn-mark-", stringify!($name)),
            view: 16.0,
            body: $body,
        };
    };
}

mark!(
    DISCLOSE_OPEN,
    r#"<path fill="currentColor" d="M5 6.5h6L8 10z"/>"#
);
mark!(
    DISCLOSE_CLOSED,
    r#"<path fill="currentColor" d="M6.5 5v6L10 8z"/>"#
);
mark!(
    SEARCH,
    r#"<g fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="9.5" cy="6.5" r="3.5"/><path d="M7 9 3 13"/></g>"#
);
mark!(
    CHIEF,
    r#"<path fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" d="M2 10.5 7 6v4.5L14 5.5"/>"#
);
mark!(
    MOON,
    r#"<path fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" d="M8 2.5a4 4 0 0 0 5.5 5.5 5.5 5.5 0 1 1-5.5-5.5Z"/>"#
);
mark!(
    FLAG,
    r#"<path fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" d="M4 2.5v11"/><path fill="currentColor" d="M4.6 2.8h8l-2 2.6 2 2.6h-8Z"/>"#
);
mark!(
    OVERVIEW,
    r#"<g fill="none" stroke="currentColor" stroke-width="1"><rect x="3.5" y="3.5" width="9" height="9"/><path d="M8 3.5v9M3.5 8h9"/></g>"#
);

macro_rules! seed {
    ($name:ident, $body:expr) => {
        static $name: Icon = Icon {
            name: concat!("attn-seed-", stringify!($name)),
            view: 32.0,
            body: concat!(
                r#"<g fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round">"#,
                $body,
                "</g>"
            ),
        };
    };
}

seed!(
    SEED_PLANTED,
    r#"<path opacity="0.42" d="M5 25.5c3-1.1 5-1.1 7 0m8 0c2-1.1 4-1.1 7 0"/><path fill="currentColor" fill-opacity="0.15" d="M20.7 9.6c2.6 3.8.7 10-4.2 12.1-3.8 1.7-6.9-.4-6-4.5 1-4.6 6.4-8.5 10.2-7.6Z"/><path stroke-width="1.1" opacity="0.62" d="M18 12.6c-2.6 1.3-4.3 3.7-4.6 6.3"/><g opacity="0.42"><path d="M9 28h2m10 0h2"/><circle cx="16" cy="27.5" r=".65" fill="currentColor" stroke="none"/></g>"#
);
seed!(
    SEED_GROWING,
    r#"<path opacity="0.42" d="M7 28h18"/><path d="M16 27V14"/><path fill="currentColor" fill-opacity="0.15" d="M15.9 20C9.6 20.3 6.1 17 6.5 11.7c5.5-.6 9.4 2.5 9.4 8.3Z"/><path stroke-width="1.1" opacity="0.62" d="m10 15 5.8 5"/><path fill="currentColor" fill-opacity="0.15" d="M16 16c.4-6.6 4.2-10 10-9.6.5 5.7-3.4 9.8-10 9.6Z"/><path stroke-width="1.1" opacity="0.62" d="m16.3 15.7 6-5.8"/>"#
);
seed!(
    SEED_PARKED,
    r#"<path opacity="0.42" d="M7 28h18"/><path d="M16 27v-3"/><path fill="currentColor" fill-opacity="0.15" d="M16 11.5c-5.6.5-8.6 5.1-6.8 9.1 1.9 4.1 11.2 4.5 13.3.4 2.1-4-1-8.8-6.5-9.5Z"/><path stroke-width="1.1" opacity="0.62" d="M16 11.7c-.7 2.4.3 4 2.7 4.7"/><path d="M11.5 18.8q1.7 1.6 3.4 0m3 .2q1.3 1.2 2.6-.2"/><path fill="currentColor" fill-opacity="0.15" d="M24.8 3.8c-1 3.8.9 5.6 4 5-1.2 3.7-6 4-7.5.9-1.2-2.6.5-5.2 3.5-5.9Z"/>"#
);
seed!(
    SEED_HARVESTED,
    r#"<path stroke-width="1.1" opacity="0.62" d="M10 15v-3a6 6 0 0 1 12 0v3"/><circle fill="currentColor" fill-opacity="0.15" cx="10.7" cy="14" r="3.4"/><path stroke-width="1.1" opacity="0.62" d="m11 10.5-.8-1.8"/><circle fill="currentColor" fill-opacity="0.15" cx="16.4" cy="11.9" r="3.4"/><path fill="currentColor" fill-opacity="0.15" stroke-width="1.1" opacity="0.62" d="M16.3 8.4c.4-2.4 2-3.1 3.5-2.5-.3 2-1.6 2.8-3.5 2.5Z"/><circle fill="currentColor" fill-opacity="0.15" cx="22" cy="14" r="3.3"/><path fill="currentColor" fill-opacity="0.15" d="M6.3 17.1h19.4l-2.4 10H8.7l-2.4-10Z"/><path d="M5.5 16.9h21"/><path stroke-width="1.1" opacity="0.62" d="m11.3 19.9.8 4.5m3.9-4.5v4.5m4.7-4.5-.8 4.5M9.5 22.1h13"/>"#
);
seed!(
    SEED_WITHERED,
    r#"<path opacity="0.42" d="M6 28.5h12"/><path d="M14 27.5c.8-4.7 2.1-8.5 2.1-13.1 0-4.8 5.9-7.4 8.5-3.7"/><path fill="currentColor" fill-opacity="0.15" d="M15.4 21c-5 .2-8.2-2.8-8.1-6.9 4.9.3 7.6 2.7 8.1 6.9Z"/><path stroke-width="1.1" opacity="0.62" d="m10.4 17.3 4.8 3.5"/><path fill="currentColor" fill-opacity="0.15" d="M24.6 10.7c-4.1 1.1-5 4.1-1.4 7.2 3.7-1.8 4.3-4.8 1.4-7.2Z"/><path fill="currentColor" fill-opacity="0.15" d="M20 27.8c1.7-3.6 5.3-4.7 8.3-2.5-1.8 3.4-5.2 4.3-8.3 2.5Z"/><path stroke-width="1.1" opacity="0.62" d="m21 27.4 4.7-1.4"/>"#
);
seed!(
    SEED_PLOT,
    r#"<path fill="currentColor" fill-opacity="0.15" d="m4 20 12-5 12 5-12 7-12-7Z"/><path opacity="0.42" d="M4 24.5 16 31l12-6.5"/><path d="M11 20v-8m0 4c-4 0-5-3-5-5 4 0 5 2 5 5Zm0-3c0-4 2-6 6-6 0 4-2 6-6 6Zm10 8v-7m0 3c0-4 2-5 5-5 0 3-2 5-5 5Z"/>"#
);
seed!(SEED_UNKNOWN, r#"<circle cx="16" cy="16" r="8"/>"#);

/// The drawing of a seed in `status`, as the Tauri app's `SeedStateIcon`, or of a plot, as its
/// `SeedPlotIcon`.
pub fn seed(status: &str) -> &'static Icon {
    match status {
        "planted" => &SEED_PLANTED,
        "growing" => &SEED_GROWING,
        "parked" => &SEED_PARKED,
        "harvested" => &SEED_HARVESTED,
        "withered" => &SEED_WITHERED,
        "plot" => &SEED_PLOT,
        _ => &SEED_UNKNOWN,
    }
}
