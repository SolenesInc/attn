:::data run="scripts/main-status.sh" every="30s"
:::

:::commands
rerun: scripts/rerun.sh {id}
:::

:::list ci.jobs
title: {name}
controls: rerun
:::
