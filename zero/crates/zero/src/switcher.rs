use crate::model::{Agent, AgentId};

pub fn filter_agents(agents: &[Agent], query: &str) -> Vec<AgentId> {
    let query = query.trim().to_lowercase();
    let mut matches: Vec<_> = agents
        .iter()
        .filter_map(|agent| {
            let haystack = format!(
                "{} {} {}",
                agent.name.to_lowercase(),
                agent.desktop,
                agent.state.label()
            );
            fuzzy_score(&haystack, &query).map(|score| (score, agent.name.as_str(), agent.id))
        })
        .collect();
    matches.sort_by(|a, b| a.0.cmp(&b.0).then_with(|| a.1.cmp(b.1)));
    matches.into_iter().map(|(_, _, id)| id).collect()
}

fn fuzzy_score(haystack: &str, needle: &str) -> Option<usize> {
    if needle.is_empty() {
        return Some(0);
    }
    let mut chars = haystack.char_indices();
    let mut first = None;
    let mut last = 0;
    for wanted in needle.chars() {
        let (index, _) = chars.find(|(_, got)| *got == wanted)?;
        first.get_or_insert(index);
        last = index;
    }
    Some(first.unwrap_or(0) + last.saturating_sub(first.unwrap_or(0)))
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use crate::model::{AgentState, Model};
    use crate::source::Event;

    use super::*;

    #[test]
    fn switcher_filter_matches_sparse_name_characters() {
        let mut model = Model::new();
        model
            .apply(vec![
                Event::AgentAdded {
                    id: 1,
                    name: "garden/claude".to_string(),
                    desktop: 1,
                    state: AgentState::Working,
                    output: Vec::new(),
                },
                Event::AgentAdded {
                    id: 2,
                    name: "hub/codex".to_string(),
                    desktop: 2,
                    state: AgentState::WaitingInput,
                    output: Vec::new(),
                },
            ])
            .unwrap();
        model
            .apply(vec![Event::StateChanged {
                id: 2,
                state: AgentState::WaitingInput,
                at: Duration::from_secs(1),
            }])
            .unwrap();
        assert_eq!(filter_agents(&model.agents, "hcx"), vec![2]);
        assert_eq!(filter_agents(&model.agents, "2 wait"), vec![2]);
    }
}
