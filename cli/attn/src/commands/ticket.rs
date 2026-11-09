use super::{Run, Stop};

fn signposts(verb: &str) -> &'static [&'static str] {
    match verb {
        "list" => &["attn seed ls"],
        "show" => &["attn seed show <seed-id>"],
        "inbox" => &["attn seed ready"],
        "new" => &[r#"attn seed plant "<title>" -m "<brief>""#],
        "comment" => &[r#"attn seed note <seed-id> -m "<note>""#],
        "attach" => &["attn seed attach <seed-id> --path <file>"],
        "attach-plan" => &["attn seed attach <seed-id> --path <file> --repo <repository>"],
        "take" => &["attn seed tend <seed-id>"],
        "subscribe" => &["attn seed watch <seed-id>"],
        "unsubscribe" => &["attn seed unwatch <seed-id>"],
        "status" => &[
            r#"attn seed note <seed-id> -m "<progress or decision needed>""#,
            r#"attn seed harvest <seed-id> -m "<outcome>""#,
            "attn seed park <seed-id>",
            r#"attn seed wither <seed-id> -m "<reason>""#,
        ],
        _ => &["attn seed --help"],
    }
}

pub fn run(_run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let verb = args.first().map_or("", String::as_str);
    for command in signposts(verb) {
        eprintln!("{command}");
    }
    Err(Stop(2))
}
