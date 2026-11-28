:::data attn="sessions"
:::

:::controls
new-session
follow
:::

{all_settled}

## Asks

:::list asks count
lead: ?
title: {from}: {subject}
press: open
:::

## Your turn

:::list turns count
lead: {state}
title: {name}
sub: {activity}
aside: {turn_opened_at|ago}
press: open
:::

::::each groups
## {label}

:::list rows
lead: {state}
title: {name}
sub: {activity}
press: open
:::
::::

## Snoozed

:::list snoozed foldable count
lead: {state}
title: {name}
aside: {snoozed_until}
press: open
controls: unsnooze
:::
