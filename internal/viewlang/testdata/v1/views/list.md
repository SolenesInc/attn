:::data run="scripts/main-status.sh" every="30s"
:::

## CI on main

:::list ci.jobs foldable count
lead: {conclusion}
title: {name}
aside: {seconds|duration}
press: open
empty: No jobs ran.
:::
