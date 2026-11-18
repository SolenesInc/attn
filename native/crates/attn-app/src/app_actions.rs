use attn_protocol::RecordAnalyticsMessage;
use serde_json::Value;

use crate::State;

/// Records one use of the app on the `app` stream while usage analytics is on.
pub(crate) fn record(state: &State, name: &str, props: &[(&str, Value)]) {
    if !crate::setting::on(&state.facts.settings, "analytics.enabled") {
        return;
    }
    let _ = state.daemon.send(&RecordAnalyticsMessage {
        stream: "app".into(),
        name: name.into(),
        props: Some(
            props
                .iter()
                .map(|(key, value)| ((*key).to_owned(), value.clone()))
                .collect(),
        ),
        ..RecordAnalyticsMessage::new()
    });
}
