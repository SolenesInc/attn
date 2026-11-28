:::data run="scripts/main-status.sh" every="30s"
:::

:::commands
rerun: scripts/rerun.sh {id}
cancel: confirm="Cancel {name}?" scripts/cancel.sh --job={id}
:::

:::list ci.jobs
title: {name}
controls: rerun, cancel
:::
