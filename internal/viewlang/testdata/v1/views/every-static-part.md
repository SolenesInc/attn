# Release 4.2

:::data file="release.json"
:::

:::status ci.state
:::

:::facts
Head | `{head.sha}` {head.subject} · {head.at|ago}
Landed today | {landed}
:::

:::controls
refresh
:::

- [x] Write the notes
- [ ] Tag the build

![The new icon](icon.png)

## CI on main

:::list ci.jobs foldable count
lead: {conclusion}
title: {name}
aside: {seconds|duration}
press: open
empty: No jobs ran.
:::

:::table landings
| Session | Change | Took |
|---|---|---|
| {session} | {subject} | {seconds|duration} |
:::

:::chart hours x=hour y=temp
:::

:::compare
![Before](before.png)
![After](after.png)
:::

::::each repositories
## {name}

:::list pulls
title: {title}
aside: {checks}
:::
::::

::::one-by-one takes.jsonl
![{name}]({file})

Take {name} runs {seconds|duration}.
::::
