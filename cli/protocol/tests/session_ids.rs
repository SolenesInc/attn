use attn_protocol::session_id::{key_of, short};
use serde::Deserialize;

#[derive(Deserialize)]
struct Line {
    r#ref: String,
    key: Option<u32>,
    short: String,
}

#[test]
fn session_references_reduce_to_the_keys_and_short_ids_of_the_shared_corpus() {
    let corpus: Vec<Line> = serde_json::from_str(include_str!(
        "../../../internal/sessionref/testdata/session_ids.json"
    ))
    .expect("the corpus is a list of lines");
    for line in &corpus {
        assert_eq!(key_of(&line.r#ref), line.key, "key of {:?}", line.r#ref);
        assert_eq!(
            short(&line.r#ref),
            line.short,
            "short id of {:?}",
            line.r#ref
        );
    }
}
