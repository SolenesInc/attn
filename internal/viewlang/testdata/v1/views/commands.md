:::data run="scripts/main-status.sh" every="30s"
:::

:::commands
rerun: scripts/rerun.sh {id}
stop: confirm="Stop {name}?" scripts/stop.sh --job={id}
:::

:::list ci.jobs
title: {name}
controls: rerun, stop
:::
