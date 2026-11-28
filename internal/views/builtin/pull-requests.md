:::data attn="pull-requests"
:::

:::controls
refresh
:::

{polling_off}{empty}

## Yours

:::list yours count
lead: {role_said}
title: {heading} {title}
aside: {ci}
press: open
controls: new-session, choose-harness, approve, merge, mute, mute-author
:::

## Review requested

::::each review
### {name}

:::list rows foldable
lead: {role_said}
title: #{number} {title}
aside: {ci}
group-aside: {count} review
group-controls: mute-repo
press: open
controls: new-session, choose-harness, approve, merge, mute, mute-author
:::
::::
