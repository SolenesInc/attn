use attn_ui::{STORIES, Showcase};
use rune::{Env, Runtime};
use serde_json::Value;

fn nodes(node: &Value, out: &mut Vec<(String, Option<String>)>) {
    if let Some(role) = node["role"].as_str() {
        out.push((role.to_owned(), node["name"].as_str().map(str::to_owned)));
    }
    for child in node["children"].as_array().into_iter().flatten() {
        nodes(child, out);
    }
}

#[test]
fn every_shared_component_has_its_role_and_a_name_in_dark_and_light() {
    let expected = [
        ("Components/List row", "list-item"),
        ("Components/Button", "button"),
        ("Components/Field", "text-input"),
        ("Components/Menu", "menu"),
        ("Components/Dialog", "dialog"),
        ("Components/Popover", "dialog"),
        ("Components/Toast", "status"),
        ("Components/Tooltip", "tooltip"),
    ];
    assert_eq!(STORIES.len(), expected.len());
    for (&(story, view), (name, role)) in STORIES.iter().zip(expected) {
        assert_eq!(story, name);
        for light in [false, true] {
            let env = Env {
                system_dark: !light,
                scale: 2.0,
            };
            let mut runtime = Runtime::new(Showcase::default(), view, (800.0, 600.0), env);
            runtime.frame(0.0);
            let mut found = Vec::new();
            nodes(&runtime.accessibility_json(), &mut found);
            assert!(
                found
                    .iter()
                    .any(|(r, n)| r == role && n.as_deref().is_some_and(|n| !n.is_empty())),
                "{story} shows no named {role}: {found:?}"
            );
        }
    }
}
