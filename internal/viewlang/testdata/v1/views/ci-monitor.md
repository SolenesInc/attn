# main

:::data run="scripts/main-status.sh" every="30s"
:::

:::status ci.state
:::

:::list ci.jobs
lead: {conclusion}
title: {name}
aside: {seconds}
:::
