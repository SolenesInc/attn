use attn_app::annotations::{Quoted, labels, terminal_payload};
use attn_app::prompts::render;
use serde_json::Value;
use sha2::{Digest, Sha256};

fn fixture(path: &str) -> Value {
    let text = std::fs::read_to_string(format!(
        "{}/../../../app/src/prompts/{path}",
        env!("CARGO_MANIFEST_DIR")
    ))
    .expect("the fixture is in the app's prompts folder");
    serde_json::from_str(&text).expect("the fixture is JSON")
}

#[test]
fn each_prompt_renders_the_bytes_go_and_the_tauri_app_render() {
    let catalog = fixture("catalog.generated.json");
    let cases = catalog["parity"].as_array().expect("parity cases");
    assert!(!cases.is_empty());
    for (i, case) in cases.iter().enumerate() {
        let (recipient, event) = (
            case["recipient"].as_str().unwrap(),
            case["event"].as_str().unwrap(),
        );
        let values: Vec<(String, String)> = case["values"]
            .as_object()
            .map(|v| {
                v.iter()
                    .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_owned()))
                    .collect()
            })
            .unwrap_or_default();
        let values: Vec<(&str, &str)> = values
            .iter()
            .map(|(k, v)| (k.as_str(), v.as_str()))
            .collect();
        let text = render(recipient, event, &values)
            .unwrap_or_else(|e| panic!("{recipient}/{event} case {i}: {e}"));
        let hash: String = Sha256::digest(text.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        assert_eq!(
            hash,
            case["sha256"].as_str().unwrap(),
            "{recipient}/{event} case {i}:\n{text}"
        );
    }
}

#[test]
fn terminal_feedback_reads_as_the_tauri_app_sends_it() {
    for (i, case) in fixture("testdata/terminal.json")
        .as_array()
        .unwrap()
        .iter()
        .enumerate()
    {
        let marks: Vec<Quoted> = case["annotations"]
            .as_array()
            .unwrap()
            .iter()
            .map(|a| Quoted {
                quote: a["quote"].as_str().unwrap(),
                label: a["quickLabelId"].as_str().unwrap(),
                comment: a["comment"].as_str().unwrap(),
                start: a["start"].as_u64().unwrap() as usize,
            })
            .collect();
        let got = terminal_payload(&marks, case["note"].as_str().unwrap()).unwrap();
        assert_eq!(got, case["expected"].as_str().unwrap(), "case {i}");
    }
}

#[test]
fn the_quick_labels_are_the_tauri_app_labels() {
    assert_eq!(
        serde_json::to_value(labels()).unwrap(),
        fixture("testdata/quick-labels.json")
    );
}
